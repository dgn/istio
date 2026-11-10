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
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"istio.io/api/annotation"
	extensions "istio.io/api/extensions/v1alpha1"
	"istio.io/api/label"
	meshv1alpha1 "istio.io/api/mesh/v1alpha1"
	networking "istio.io/api/networking/v1alpha3"
	security "istio.io/api/security/v1beta1"
	telemetry "istio.io/api/telemetry/v1alpha1"
	"sigs.k8s.io/yaml"

	"istio.io/istio/pkg/config/constants"
	"istio.io/istio/pkg/test/env"
)

// Surface entry formats:
//
//	<IstioKind>.<field>.<field>      Istio proto field (spec is implicit), e.g. AuthorizationPolicy.rules.to.operation.paths
//	<IstioKind>.<path>=<ENUM_VALUE>  Istio enum value, e.g. AuthorizationPolicy.action=DENY
//	annotation:<name>                Istio annotation (istio/api registry) on a resource that shapes proxy behavior
//	label:<name>                     Istio label (istio/api registry), same resources as annotations
//	gateway-api-ext:<what>           Istio extension of Gateway API (options, extra kinds, overlays)
//	MeshConfig.<field>, ProxyConfig.<field>  mesh-wide configuration
//	flag:<ENV_NAME>                  istiod feature flag referenced by a proxy config code path
//	interaction:<id>                 multi-resource semantics from interactions.yaml (not a single field)
//
// Gateway API fields themselves are NOT part of the surface: their semantics are covered by the
// upstream Gateway API conformance suite, which TestGatewayAPIConformance runs against every proxy.
// Only what Istio adds on top is tracked here.

// istioKinds are the Istio APIs that configure proxies in any role (see design doc §7).
//
// WasmPlugin is ordinary surface: its user-facing API (an OCI/HTTP module, config, phase, match,
// targetRefs) has no Envoy-isms, so any proxy that runs Wasm may implement it. EnvoyFilter is part
// of the surface so a new field is noticed, but its whole subtree is exempted in exemptions.yaml:
// it patches raw Envoy config and is catalogued as impl-specific features. TrafficExtension is not
// tracked at all (Envoy-only, impl-specific features).
var istioKinds = map[string]protoreflect.ProtoMessage{
	"AuthorizationPolicy":   &security.AuthorizationPolicy{},
	"RequestAuthentication": &security.RequestAuthentication{},
	"PeerAuthentication":    &security.PeerAuthentication{},
	"Gateway":               &networking.Gateway{},
	"VirtualService":        &networking.VirtualService{},
	"DestinationRule":       &networking.DestinationRule{},
	"ServiceEntry":          &networking.ServiceEntry{},
	"Sidecar":               &networking.Sidecar{},
	"WorkloadEntry":         &networking.WorkloadEntry{},
	"WorkloadGroup":         &networking.WorkloadGroup{},
	"EnvoyFilter":           &networking.EnvoyFilter{},
	"Telemetry":             &telemetry.Telemetry{},
	"WasmPlugin":            &extensions.WasmPlugin{},
}

// meshKinds are mesh-wide configuration APIs. They are walked in full; subtrees that cannot affect
// gateways are exempted by prefix in exemptions.yaml (e.g. "MeshConfig.outboundTrafficPolicy.*").
// ProxyConfig reaches gateways via MeshConfig.defaultConfig, the ProxyConfig CRD, or the
// proxy.istio.io/config annotation on the Gateway.
var meshKinds = map[string]protoreflect.ProtoMessage{
	"MeshConfig":  &meshv1alpha1.MeshConfig{},
	"ProxyConfig": &meshv1alpha1.ProxyConfig{},
}

// proxyConfigCodePaths are the istiod code paths that generate configuration for proxies in any
// role: the Gateway API translators and deployment controllers, xDS generation for Envoy (gateways,
// sidecars, waypoints) and for ztunnel (the workload API), and the push context they read from.
// Every istiod feature flag (pilot/pkg/features) referenced from them is part of the surface
// ("flag:<ENV_NAME>"); flags that only steer the control plane itself are exempted with a reason.
// A trailing "/..." includes subpackages.
var proxyConfigCodePaths = []string{
	"pilot/pkg/config/kube/gateway",
	"pilot/pkg/config/kube/gatewaycommon",
	"pilot/pkg/config/kube/agentgateway",
	"pilot/pkg/networking/...",
	"pilot/pkg/model",
	"pilot/pkg/xds/...",
	"pilot/pkg/security/...",
	"pilot/pkg/serviceregistry/ambient",
}

// gatewayAPIExtensions are Istio-specific extensions of Gateway API semantics. Keys that appear as
// string literals in the Gateway API translators are cross-checked against this list by
// TestExtensionKeysTracked, so a new option key in istiod cannot be added silently.
var gatewayAPIExtensions = []string{
	// Listener tls.options (pilot/pkg/config/kube/gateway/conversion.go).
	"gateway-api-ext:tls-option:gateway.istio.io/tls-terminate-mode=MUTUAL",
	"gateway-api-ext:tls-option:gateway.istio.io/tls-terminate-mode=OPTIONAL_MUTUAL",
	"gateway-api-ext:tls-option:gateway.istio.io/tls-terminate-mode=ISTIO_SIMPLE",
	"gateway-api-ext:tls-option:gateway.istio.io/tls-terminate-mode=ISTIO_MUTUAL",
	"gateway-api-ext:tls-option:gateway.istio.io/tls-cipher-suites",
	"gateway-api-ext:tls-option:" + constants.ListenerModeOption + "=auto-passthrough",
	// Extra backendRef kinds.
	"gateway-api-ext:backendref-kind:networking.istio.io/Hostname",
	// Extra route kinds on listeners.
	"gateway-api-ext:tls-terminate-listener:TCPRoute",
	// parametersRef / GatewayClass-defaults ConfigMap overlay keys (gatewaycommon/deploymentcontroller.go).
	"gateway-api-ext:parametersref-overlay:deployment",
	"gateway-api-ext:parametersref-overlay:service",
	"gateway-api-ext:parametersref-overlay:serviceAccount",
	"gateway-api-ext:parametersref-overlay:horizontalPodAutoscaler",
	"gateway-api-ext:parametersref-overlay:podDisruptionBudget",
	"gateway-api-ext:gatewayclass-defaults-configmap:gateway.istio.io/defaults-for-class",
	// Gateway annotations read by istiod but not registered for the Gateway resource in istio/api.
	"annotation:proxy.istio.io/config",
	"annotation:networking.istio.io/address-type",
	// Manual deployment: a Hostname address binds to an existing Service's pods.
	"gateway-api-ext:addresses-hostname-manual-deployment",
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

// Surface returns the sorted list of all surface entries.
func Surface() []string {
	set := map[string]struct{}{}
	add := func(s string) { set[s] = struct{}{} }

	for kind, m := range istioKinds {
		walkProto(kind, m.ProtoReflect().Descriptor(), map[protoreflect.FullName]bool{}, add)
	}
	for kind, m := range meshKinds {
		walkProto(kind, m.ProtoReflect().Descriptor(), map[protoreflect.FullName]bool{}, add)
	}
	for _, f := range FeatureFlags() {
		add("flag:" + f)
	}
	for _, e := range gatewayAPIExtensions {
		add(e)
	}
	for _, a := range annotation.AllResourceAnnotations() {
		if !a.Deprecated && appliesToProxyResource(a.Resources) {
			add("annotation:" + a.Name)
		}
	}
	for _, l := range label.AllResourceLabels() {
		if !l.Deprecated && appliesToProxyResource(l.Resources) {
			add("label:" + l.Name)
		}
	}
	interactions, err := Interactions()
	if err != nil {
		panic(err)
	}
	for _, i := range interactions {
		add("interaction:" + i.ID)
	}

	out := make([]string, 0, len(set))
	for s := range set {
		if collapsed(s) {
			continue
		}
		out = append(out, s)
	}
	sort.Strings(out)
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
const InteractionsFile = "tests/integration/conformance/proxy/coverage/interactions.yaml"

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

// FeatureFlags returns the env var names of all pilot/pkg/features flags referenced by
// proxyConfigCodePaths. Flags that cannot be resolved to an env.Register call are reported as
// "features.<Ident>".
func FeatureFlags() []string {
	names := featureFlagNames()
	used := map[string]bool{}
	for _, p := range proxyConfigCodePaths {
		for _, f := range goFiles(filepath.Join(env.IstioSrc, p)) {
			af, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
			if err != nil {
				panic(err)
			}
			alias := ""
			for _, imp := range af.Imports {
				if strings.Trim(imp.Path.Value, `"`) == "istio.io/istio/pilot/pkg/features" {
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
					} else {
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

// featureFlagNames maps pilot/pkg/features identifiers to the env var they are registered as.
func featureFlagNames() map[string]string {
	out := map[string]string{}
	for _, f := range goFiles(filepath.Join(env.IstioSrc, "pilot/pkg/features")) {
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			panic(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			vs, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, v := range vs.Values {
				if i >= len(vs.Names) {
					break
				}
				if name := registeredEnvName(v); name != "" {
					out[vs.Names[i].Name] = name
				}
			}
			return true
		})
	}
	return out
}

// registeredEnvName finds env.Register("NAME", ...) (or env.RegisterXVar) within an expression, or a
// package-local wrapper such as registerAmbient("NAME", ...).
func registeredEnvName(e ast.Expr) string {
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
		if bl, ok := call.Args[0].(*ast.BasicLit); ok && bl.Kind == token.STRING {
			name, _ = strconv.Unquote(bl.Value)
		}
		return name == ""
	})
	return name
}

// goFiles returns the non-test Go files of a file, a package directory, or (with a trailing "/...")
// a directory tree. testdata directories are skipped.
func goFiles(path string) []string {
	if strings.HasSuffix(path, ".go") {
		return []string{path}
	}
	if root, ok := strings.CutSuffix(path, "/..."); ok {
		var out []string
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "testdata" {
				return filepath.SkipDir
			}
			if d.IsDir() {
				out = append(out, goFiles(p)...)
			}
			return nil
		})
		if err != nil {
			panic(err)
		}
		return out
	}
	files, _ := filepath.Glob(filepath.Join(path, "*.go"))
	out := files[:0]
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			out = append(out, f)
		}
	}
	return out
}
