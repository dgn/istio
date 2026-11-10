// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package coverage computes the API surface the proxy conformance suite must cover, and checks the
// suite's declared coverage against it. It has no build tag and needs no cluster, so it runs with the
// regular unit tests on every PR: adding a new API field (or bumping istio/api or Gateway API) fails
// the check until the field is covered by a feature or explicitly exempted with a reason.
package coverage

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"sigs.k8s.io/yaml"

	"istio.io/api/annotation"
	"istio.io/api/label"
	meshv1alpha1 "istio.io/api/mesh/v1alpha1"
	"istio.io/istio/pilot/pkg/config/kube/gatewayext"
	"istio.io/istio/pkg/config/schema/collections"
	"istio.io/istio/pkg/maps"
	"istio.io/istio/pkg/slices"
	"istio.io/istio/pkg/test/env"
	"istio.io/istio/pkg/util/sets"
)

// Surface entry formats:
//
//	<IstioKind>.<field>.<field>      Istio proto field (spec is implicit), e.g. AuthorizationPolicy.rules.to.operation.paths
//	<IstioKind>.<path>=<ENUM_VALUE>  Istio enum value, e.g. AuthorizationPolicy.action=DENY
//	annotation:<name>                Istio annotation (istio/api registry) on a resource that shapes proxy behavior
//	label:<name>                     Istio label (istio/api registry), same resources as annotations
//	gateway-api-ext:<what>           Istio extension of Gateway API (options, extra kinds, overlays)
//	MeshConfig.<field>, ProxyConfig.<field>  mesh-wide configuration
//	flag:<component>:<ENV_NAME>      env var flag of a data-plane component (FlagComponents)
//	interaction:<id>                 multi-resource semantics from interactions.yaml (not a single field)
//
// Gateway API fields themselves are NOT part of the surface: their semantics are covered by the
// upstream Gateway API conformance suite, which TestGatewayAPIConformance runs against every proxy.
// Only what Istio adds on top is tracked here.

// istioKinds are the Istio config kinds istiod turns into proxy config: every kind in
// collections.Pilot, keyed by kind, so a new CRD istiod watches is tracked without anyone listing it.
//
// WasmPlugin is ordinary surface: its user-facing API (an OCI/HTTP module, config, phase, match,
// targetRefs) has no Envoy-isms, so any proxy that runs Wasm may implement it. EnvoyFilter and
// TrafficExtension are part of the surface so a new field is noticed, but their whole subtrees are
// exempted in exemptions.yaml: they configure Envoy directly and are cataloged as impl-specific
// features. The ProxyConfig CRD shares its kind with the mesh ProxyConfig (meshKinds); their fields
// are merged under "ProxyConfig".
func istioKinds() map[string]protoreflect.ProtoMessage {
	out := map[string]protoreflect.ProtoMessage{}
	for _, s := range collections.Pilot.All() {
		m, ok := s.MustNewInstance().(protoreflect.ProtoMessage)
		if !ok {
			panic(fmt.Sprintf("collections.Pilot kind %s is not a proto message", s.Kind()))
		}
		out[s.Kind()] = m
	}
	return out
}

// meshKinds are mesh-wide configuration APIs. They are walked in full; subtrees that cannot affect
// gateways are exempted by prefix in exemptions.yaml (e.g. "MeshConfig.outboundTrafficPolicy.*").
// ProxyConfig reaches gateways via MeshConfig.defaultConfig, the ProxyConfig CRD, or the
// proxy.istio.io/config annotation on the Gateway.
var meshKinds = map[string]protoreflect.ProtoMessage{
	"MeshConfig":  &meshv1alpha1.MeshConfig{},
	"ProxyConfig": &meshv1alpha1.ProxyConfig{},
}

// proxyConfigRoots are the istiod packages that generate configuration for proxies in any role: the
// Gateway API translators and deployment controllers, xDS generation for Envoy (gateways, sidecars,
// waypoints) and for ztunnel (the workload API), and the push context they read from. Every istiod
// feature flag (pilot/pkg/features) referenced from them or from any istio.io/istio package they
// import (proxyConfigPackages) is part of the surface ("flag:istiod:<ENV_NAME>"); flags that only steer the
// control plane itself are exempted with a reason. A trailing "/..." includes subpackages.
var proxyConfigRoots = []string{
	"pilot/pkg/config/kube/gateway",
	"pilot/pkg/config/kube/gatewaycommon",
	"pilot/pkg/config/kube/agentgateway",
	"pilot/pkg/networking/...",
	"pilot/pkg/model",
	"pilot/pkg/xds/...",
	"pilot/pkg/security/...",
	"pilot/pkg/serviceregistry/ambient",
}

// Components whose env var flags are part of the surface, as "flag:<component>:<ENV_NAME>".
const (
	ComponentIstiod   = "istiod"   // pilot/pkg/features flags referenced by proxyConfigPackages
	ComponentAgent    = "agent"    // pilot-agent, next to every Envoy proxy
	ComponentCNI      = "cni"      // istio-cni and its ambient node agent
	ComponentIPTables = "iptables" // istio-iptables/istio-nftables traffic redirection, run by the agent or the CNI
)

// flagComponent finds the flags of a component other than istiod: every env.Register call in its
// roots and the istio.io/istio packages they import, transitively, except in trees that belong to
// another component (whose flags are that component's). A flag registered in a package several
// components import (e.g. pkg/security) is an entry of each of them.
type flagComponent struct {
	name   string
	roots  []string
	others []string
}

var flagComponents = []flagComponent{
	{ComponentAgent, []string{"pilot/cmd/pilot-agent/..."}, []string{"pilot/pkg/", "cni/", "tools/"}},
	{ComponentCNI, []string{"cni/cmd/...", "cni/pkg/..."}, []string{"pilot/", "tools/"}},
	{ComponentIPTables, []string{"tools/istio-iptables/...", "tools/istio-nftables/..."}, []string{"pilot/", "cni/"}},
}

const (
	modulePath   = "istio.io/istio/"
	featuresPath = modulePath + "pilot/pkg/features"
)

// proxyConfigPackages returns the directories (relative to the repository root) of proxyConfigRoots
// and of every istio.io/istio package they import, transitively. Imports are read from source
// without evaluating build constraints, so the set is a superset of any one build.
func proxyConfigPackages() []string {
	return importClosure(proxyConfigRoots)
}

// importClosure returns the directories (relative to the repository root) of roots and of every
// istio.io/istio package they import, transitively, except pilot/pkg/features. A trailing "/..." on a
// root includes its subpackages.
func importClosure(roots []string) []string {
	seen := map[string]bool{}
	var queue []string
	visit := func(dir string) {
		if !seen[dir] && dir != strings.TrimPrefix(featuresPath, modulePath) {
			seen[dir] = true
			queue = append(queue, dir)
		}
	}
	for _, root := range roots {
		base, tree := strings.CutSuffix(root, "/...")
		if !tree {
			visit(base)
			continue
		}
		err := filepath.WalkDir(filepath.Join(env.IstioSrc, base), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "testdata" {
				return filepath.SkipDir
			}
			if d.IsDir() {
				rel, _ := filepath.Rel(env.IstioSrc, p)
				visit(filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			panic(err)
		}
	}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		for _, f := range goFiles(filepath.Join(env.IstioSrc, dir)) {
			af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
			if err != nil {
				panic(err)
			}
			for _, imp := range af.Imports {
				if p, ok := strings.CutPrefix(strings.Trim(imp.Path.Value, `"`), modulePath); ok {
					visit(p)
				}
			}
		}
	}
	return slices.Sort(maps.Keys(seen))
}

// collapsedSubtrees are fields whose type repeats a subtree that is already tracked elsewhere. The
// field itself stays in the surface (its own semantics, e.g. subset- or port-level precedence, need a
// feature), but its descendants are dropped: they are the same fields as the canonical subtree.
var collapsedSubtrees = map[string]string{
	"DestinationRule.subsets.trafficPolicy":           "DestinationRule.trafficPolicy",
	"DestinationRule.trafficPolicy.portLevelSettings": "DestinationRule.trafficPolicy",
	"MeshConfig.defaultTrafficPolicy":                 "DestinationRule.trafficPolicy",
	"WorkloadGroup.template":                          "WorkloadEntry",
}

func collapsed(entry string) bool {
	for prefix := range collapsedSubtrees {
		if strings.HasPrefix(entry, prefix+".") || strings.HasPrefix(entry, prefix+"=") {
			// Keep port selectors of portLevelSettings: they are its own fields.
			if strings.HasPrefix(entry, "DestinationRule.trafficPolicy.portLevelSettings.port") {
				return false
			}
			return true
		}
	}
	return false
}

// Sources of surface entries. All but SourceManual are derived from istio/api or istiod's code.
const (
	SourceProto              = "proto"               // fields and enum values of istioKinds and meshKinds
	SourceAnnotationRegistry = "annotation-registry" // istio/api annotations on proxyResources
	SourceLabelRegistry      = "label-registry"      // istio/api labels on proxyResources
	SourceFeatureFlags       = "feature-flags"       // ComponentFlags
	SourceExtensionRegistry  = "extension-registry"  // gatewayext.Registry(), next to the translators
	SourceManual             = "manual"              // interactions.yaml
)

// Surface returns the sorted list of all surface entries.
func Surface() []string {
	return slices.Sort(maps.Keys(SurfaceSources()))
}

// SurfaceSources maps every surface entry to its source (surface.yaml). An entry that is listed by
// hand and also found by a programmatic source gets the programmatic source, so SourceManual marks
// exactly the entries that are tracked only because someone listed them.
func SurfaceSources() map[string]string {
	out := map[string]string{}
	// The first source to add an entry wins, so programmatic sources go first.
	adder := func(source string) func(string) {
		return func(entry string) {
			if _, ok := out[entry]; !ok && !collapsed(entry) {
				out[entry] = source
			}
		}
	}

	addProto := adder(SourceProto)
	for kind, m := range istioKinds() {
		walkProto(kind, m.ProtoReflect().Descriptor(), map[protoreflect.FullName]bool{}, addProto)
	}
	for kind, m := range meshKinds {
		walkProto(kind, m.ProtoReflect().Descriptor(), map[protoreflect.FullName]bool{}, addProto)
	}
	addFlag := adder(SourceFeatureFlags)
	for _, f := range ComponentFlags() {
		addFlag("flag:" + f)
	}
	addAnnotation := adder(SourceAnnotationRegistry)
	for _, a := range annotation.AllResourceAnnotations() {
		if !a.Deprecated && appliesToProxyResource(a.Resources) {
			addAnnotation("annotation:" + a.Name)
		}
	}
	addLabel := adder(SourceLabelRegistry)
	for _, l := range label.AllResourceLabels() {
		if !l.Deprecated && appliesToProxyResource(l.Resources) {
			addLabel("label:" + l.Name)
		}
	}

	addExtension := adder(SourceExtensionRegistry)
	for _, e := range gatewayext.Registry() {
		addExtension(e.Entry)
	}
	addManual := adder(SourceManual)
	interactions, err := Interactions()
	if err != nil {
		panic(err)
	}
	for _, i := range interactions {
		addManual("interaction:" + i.ID)
	}
	return out
}

// proxyResources are the resources whose Istio annotations and labels can shape what a proxy does:
// the workloads a proxy runs for or next to (Pod, WorkloadEntry), the destinations it routes to
// (Service, ServiceEntry, and Namespace, from which both inherit), and the Gateway it is deployed
// for. "Any" annotations apply to all of them. Annotations and labels on other resources
// (MutatingWebhookConfiguration, Node, Deployment, ...) are installation or bookkeeping metadata.
var proxyResources = map[string]bool{
	"Any": true, "Pod": true, "Namespace": true, "Service": true, "ServiceEntry": true, "Gateway": true, "WorkloadEntry": true,
}

func appliesToProxyResource[T fmt.Stringer](resources []T) bool {
	for _, r := range resources {
		if proxyResources[r.String()] {
			return true
		}
	}
	return false
}

// InteractionsFile is the reviewed inventory of multi-resource semantics (interactions.yaml).
const InteractionsFile = CoverageDir + "/interactions.yaml"

// Interaction is a behavior that emerges from how several resources (or several fields of different
// resources) combine, e.g. AuthorizationPolicy evaluation order or DestinationRule subset precedence.
// No single API field represents it, so the field surface alone cannot tell whether it is tested.
type Interaction struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	// Roles are the roles in which the interaction applies. Non-ingress roles must each cover it
	// (an implicit roleScope entry), as ingress coverage is global.
	Roles []string `json:"roles"`
	// Source documents the semantics: an istio.io URL or a path in this repository.
	Source string `json:"source"`
	// CoveredBy lists existing features (IDs or aliases) that test the interaction without listing
	// it in their Covers. Prefer Covers: []string{"interaction:<id>"} in the feature itself.
	CoveredBy []string `json:"coveredBy,omitempty"`
}

// Interactions reads interactions.yaml.
func Interactions() ([]Interaction, error) {
	b, err := os.ReadFile(filepath.Join(env.IstioSrc, InteractionsFile))
	if err != nil {
		return nil, err
	}
	var out []Interaction
	if err := yaml.UnmarshalStrict(b, &out); err != nil {
		return nil, fmt.Errorf("%s: %v", InteractionsFile, err)
	}
	return out, nil
}

func walkProto(prefix string, md protoreflect.MessageDescriptor, stack map[protoreflect.FullName]bool, add func(string)) {
	if stack[md.FullName()] {
		return // recursive type (e.g. VirtualService delegate chains); covered at first occurrence
	}
	stack[md.FullName()] = true
	defer delete(stack, md.FullName())
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		walkProtoField(prefix, fields.Get(i), stack, add)
	}
}

func walkProtoField(prefix string, f protoreflect.FieldDescriptor, stack map[protoreflect.FullName]bool, add func(string)) {
	if opts, ok := f.Options().(*descriptorpb.FieldOptions); ok && opts.GetDeprecated() {
		return
	}
	path := prefix + "." + f.JSONName()
	add(path)
	if f.IsMap() {
		f = f.MapValue()
	}
	switch f.Kind() {
	case protoreflect.EnumKind:
		vals := f.Enum().Values()
		for i := 0; i < vals.Len(); i++ {
			v := vals.Get(i)
			if opts, ok := v.Options().(*descriptorpb.EnumValueOptions); ok && opts.GetDeprecated() {
				continue
			}
			name := string(v.Name())
			if v.Number() == 0 && strings.Contains(name, "UNSPECIFIED") {
				continue
			}
			add(path + "=" + name)
		}
	case protoreflect.MessageKind, protoreflect.GroupKind:
		md := f.Message()
		if strings.HasPrefix(string(md.FullName()), "google.protobuf.") {
			return // well-known types are leaves
		}
		walkProto(path, md, stack, add)
	}
}

// SetIn returns the surface keys that an Istio config spec sets, in the form walkProto generates
// ("DestinationRule.trafficPolicy.tls.mode=SIMPLE"). Field names are
// matched by JSON or proto name; map keys are skipped as in the surface. An absent enum field whose
// zero value is meaningful (not *_UNSPECIFIED, e.g. AuthorizationPolicy.action=ALLOW) counts as set
// to it. ok is false for kinds that are not in istioKinds.
func SetIn(kind string, spec map[string]any) (keys map[string]bool, ok bool) {
	m, ok := istioKinds()[kind]
	if !ok {
		return nil, false
	}
	keys = map[string]bool{}
	setInMessage(kind, m.ProtoReflect().Descriptor(), spec, keys)
	return keys, true
}

func setInMessage(prefix string, md protoreflect.MessageDescriptor, obj map[string]any, keys map[string]bool) {
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		path := prefix + "." + f.JSONName()
		v, present := obj[f.JSONName()]
		if !present {
			v, present = obj[string(f.Name())]
		}
		if !present {
			if f.Kind() == protoreflect.EnumKind && !f.IsList() && !f.IsMap() && f.ContainingOneof() == nil {
				if zero := f.Enum().Values().ByNumber(0); zero != nil && !strings.Contains(string(zero.Name()), "UNSPECIFIED") {
					keys[path+"="+string(zero.Name())] = true
				}
			}
			continue
		}
		keys[path] = true
		var values []any
		switch {
		case f.IsMap():
			m, _ := v.(map[string]any)
			for _, mv := range m {
				values = append(values, mv)
			}
			f = f.MapValue()
		case f.IsList():
			values, _ = v.([]any)
		default:
			values = []any{v}
		}
		for _, val := range values {
			switch f.Kind() {
			case protoreflect.EnumKind:
				if s, ok := val.(string); ok {
					keys[path+"="+s] = true
				}
			case protoreflect.MessageKind, protoreflect.GroupKind:
				if child, ok := val.(map[string]any); ok && !strings.HasPrefix(string(f.Message().FullName()), "google.protobuf.") {
					setInMessage(path, f.Message(), child, keys)
				}
			}
		}
	}
}

// Field returns the proto field a proto surface key names and, for a "<path>=<VALUE>" key, the enum
// value, so tools can show the field's documentation. ok is false for keys of other sources.
func Field(key string) (fd protoreflect.FieldDescriptor, ev protoreflect.EnumValueDescriptor, ok bool) {
	path, value, isEnum := strings.Cut(key, "=")
	kind, rest, found := strings.Cut(path, ".")
	if !found {
		return nil, nil, false
	}
	// "ProxyConfig" is both a CRD and a mesh API (see istioKinds); try both.
	var roots []protoreflect.ProtoMessage
	if m, ok := istioKinds()[kind]; ok {
		roots = append(roots, m)
	}
	if m, ok := meshKinds[kind]; ok {
		roots = append(roots, m)
	}
	for _, root := range roots {
		if fd = fieldAt(root.ProtoReflect().Descriptor(), strings.Split(rest, ".")); fd == nil {
			continue
		}
		if !isEnum {
			return fd, nil, true
		}
		if fd.Enum() != nil {
			if ev = fd.Enum().Values().ByName(protoreflect.Name(value)); ev != nil {
				return fd, ev, true
			}
		}
	}
	return nil, nil, false
}

// fieldAt follows JSON field names from md, through map values, as walkProto does.
func fieldAt(md protoreflect.MessageDescriptor, names []string) protoreflect.FieldDescriptor {
	fd := md.Fields().ByJSONName(names[0])
	if fd == nil || len(names) == 1 {
		return fd
	}
	next := fd
	if next.IsMap() {
		next = next.MapValue()
	}
	if next.Message() == nil {
		return nil
	}
	return fieldAt(next.Message(), names[1:])
}

// ComponentFlags returns the flags of every component as "<component>:<ENV_NAME>", sorted.
func ComponentFlags() []string {
	var out []string
	for _, f := range FeatureFlags() {
		out = append(out, ComponentIstiod+":"+f)
	}
	for _, c := range flagComponents {
		names := map[string]bool{}
		for _, dir := range importClosure(c.roots) {
			if !slices.ContainsFunc(c.others, func(o string) bool { return strings.HasPrefix(dir, o) }) {
				for _, n := range registeredEnvNames(dir) {
					names[n] = true
				}
			}
		}
		for n := range names {
			out = append(out, c.name+":"+n)
		}
	}
	sort.Strings(out)
	return out
}

// registeredEnvNames returns the env vars registered in the package directory dir.
func registeredEnvNames(dir string) []string {
	var out []string
	for _, f := range goFiles(filepath.Join(env.IstioSrc, dir)) {
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			panic(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			if vs, ok := n.(*ast.ValueSpec); ok {
				for _, v := range vs.Values {
					if name := registeredEnvName(v, nil); name != "" {
						out = append(out, name)
					}
				}
			}
			return true
		})
	}
	return out
}

// FeatureFlags returns the env var names of all pilot/pkg/features flags referenced by
// proxyConfigPackages. Flags that cannot be resolved to an env.Register call are reported as
// "features.<Ident>".
func FeatureFlags() []string {
	names, consts := featureFlagNames()
	used := map[string]bool{}
	for _, p := range proxyConfigPackages() {
		for _, f := range goFiles(filepath.Join(env.IstioSrc, p)) {
			af, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
			if err != nil {
				panic(err)
			}
			alias := ""
			for _, imp := range af.Imports {
				if strings.Trim(imp.Path.Value, `"`) == featuresPath {
					alias = "features"
					if imp.Name != nil {
						alias = imp.Name.Name
					}
				}
			}
			if alias == "" {
				continue
			}
			ast.Inspect(af, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == alias {
					if name, ok := names[sel.Sel.Name]; ok {
						used[name] = true
					} else if !consts.Contains(sel.Sel.Name) {
						used["features."+sel.Sel.Name] = true
					}
				}
				return true
			})
		}
	}
	out := make([]string, 0, len(used))
	for n := range used {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// featureFlagNames maps pilot/pkg/features identifiers to the env var they are registered as, and
// reports the package's constants (enum values, name constants), which are not flags themselves.
func featureFlagNames() (names map[string]string, consts sets.String) {
	names = map[string]string{}
	consts = sets.New[string]()
	strConsts := map[string]string{}
	type spec struct {
		name  string
		value ast.Expr
	}
	var vars []spec
	for _, f := range goFiles(filepath.Join(env.IstioSrc, "pilot/pkg/features")) {
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			panic(err)
		}
		for _, d := range af.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, s := range gd.Specs {
				vs, ok := s.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, n := range vs.Names {
					var v ast.Expr
					if i < len(vs.Values) {
						v = vs.Values[i]
					}
					if bl, ok := v.(*ast.BasicLit); ok && bl.Kind == token.STRING {
						strConsts[n.Name], _ = strconv.Unquote(bl.Value)
					}
					if gd.Tok == token.CONST {
						consts.Insert(n.Name)
					} else if v != nil {
						vars = append(vars, spec{n.Name, v})
					}
				}
			}
		}
	}
	for _, v := range vars {
		if name := registeredEnvName(v.value, strConsts); name != "" {
			names[v.name] = name
		}
	}
	// Accessors wrapping a registered var (e.g. TraceSampling = func() {... traceSamplingVar.Get() ...})
	// stand for that var's env name.
	for changed := true; changed; {
		changed = false
		for _, v := range vars {
			if _, ok := names[v.name]; ok {
				continue
			}
			ast.Inspect(v.value, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && id.Name != v.name {
					if name, ok := names[id.Name]; ok {
						names[v.name] = name
						changed = true
						return false
					}
				}
				return names[v.name] == ""
			})
		}
	}
	return names, consts
}

// registeredEnvName finds env.Register("NAME", ...) (or env.RegisterXVar) within an expression, or a
// package-local wrapper such as registerAmbient("NAME", ...). The name may be a string constant.
func registeredEnvName(e ast.Expr, strConsts map[string]string) string {
	var name string
	ast.Inspect(e, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || name != "" {
			return name == ""
		}
		if len(call.Args) == 0 {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			if x, ok := fun.X.(*ast.Ident); !ok || x.Name != "env" || !strings.HasPrefix(fun.Sel.Name, "Register") {
				return true
			}
		case *ast.IndexExpr: // generic wrapper with explicit type argument
			if id, ok := fun.X.(*ast.Ident); !ok || !strings.HasPrefix(id.Name, "register") {
				return true
			}
		case *ast.Ident:
			if !strings.HasPrefix(fun.Name, "register") {
				return true
			}
		default:
			return true
		}
		switch a := call.Args[0].(type) {
		case *ast.BasicLit:
			if a.Kind == token.STRING {
				name, _ = strconv.Unquote(a.Value)
			}
		case *ast.Ident:
			name = strConsts[a.Name]
		}
		return name == ""
	})
	return name
}

// goFiles returns the non-test Go files of a package directory.
func goFiles(dir string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	return slices.Filter(files, func(f string) bool { return !strings.HasSuffix(f, "_test.go") })
}

// istioKeyPattern matches Istio annotation/option keys used as string literals.
var istioKeyPattern = regexp.MustCompile(`^(gateway|networking|proxy|sidecar|security|telemetry)\.istio\.io/[A-Za-z0-9.-]+$`)

// translatorDirs are the Gateway API translators and deployment controllers for all gateway
// implementations, and the registry of their Istio-specific extensions.
var translatorDirs = []string{
	"pilot/pkg/config/kube/gateway",
	"pilot/pkg/config/kube/gatewaycommon",
	"pilot/pkg/config/kube/agentgateway",
	"pilot/pkg/config/kube/gatewayext",
}

// ExtensionKeys returns the Istio-specific keys (annotations, tls options) that appear as string
// literals in translatorDirs. TestExtensionKeysTracked requires each to be in a surface entry or
// exempted as "key:<key>", so a key added to a translator without a gatewayext.Registry() entry is
// noticed.
func ExtensionKeys() (map[string]bool, error) {
	keys := map[string]bool{}
	for _, d := range translatorDirs {
		for _, f := range goFiles(filepath.Join(env.IstioSrc, d)) {
			af, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
			if err != nil {
				return nil, err
			}
			ast.Inspect(af, func(n ast.Node) bool {
				if bl, ok := n.(*ast.BasicLit); ok && bl.Kind == token.STRING {
					if v, err := strconv.Unquote(bl.Value); err == nil && istioKeyPattern.MatchString(v) {
						keys[v] = true
					}
				}
				return true
			})
		}
	}
	return keys, nil
}

// checkSource requires an istio.io documentation URL or an existing path in this repository.
func checkSource(source string) error {
	switch {
	case source == "":
		return fmt.Errorf("needs a source (istio.io URL or repository path)")
	case strings.HasPrefix(source, "https://istio.io/"):
		return nil
	case strings.Contains(source, "://"):
		return fmt.Errorf("source %q must be an https://istio.io/ page or a repository path", source)
	}
	path, _, _ := strings.Cut(source, "#")
	path, _, _ = strings.Cut(path, ":")
	if _, err := os.Stat(filepath.Join(env.IstioSrc, path)); err != nil {
		return fmt.Errorf("source path %q does not exist", source)
	}
	return nil
}
