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

package coverage

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"istio.io/istio/pkg/maps"
	"istio.io/istio/pkg/slices"
)

var update = flag.Bool("update", false,
	"Regenerate catalog.yaml, surface.yaml, coverage.yaml and summary.yaml, drop stale or redundant exemptions, and lower the ratchets. "+
		"Ratchets are never raised, so new uncovered surface still fails the check.")

var raiseTodo = flag.Bool("raise-todo", false,
	"With -update, allow the ratchets (maxTodo, maxRoleGaps, maxUnknown) to increase. Only for deliberate surface "+
		"expansions (e.g. tracking a new API); never to silence new fields. Reviewers see the increase in the diff.")

// suiteDir is the suite, relative to this package (the working directory of its tests).
const suiteDir = ".."

// catalogFile is the generated feature catalog.
var catalogFile = filepath.Join(DataDir, "catalog.yaml")

func readSuite(t *testing.T) *Suite {
	t.Helper()
	s, err := ReadSuite(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestCoverage resolves the API surface (sources) against the features (tests) and the checked-in
// decisions (exemptions.yaml, scope.yaml, ratchets.yaml), and keeps the generated views (catalog.yaml,
// surface.yaml, coverage.yaml, summary.yaml) in sync. It fails when:
//   - a new field/enum value/annotation/flag appears (e.g. an istio/api bump) and nobody decided
//     about it, so the backlog exceeds maxTodo or a role's gaps exceed maxRoleGaps
//   - a feature declares coverage of something that doesn't exist (typo or removed field)
//   - an exemption is stale, redundant, or has no real reason
//   - a generated view is out of date
func TestCoverage(t *testing.T) {
	sources := SurfaceSources()
	suite := readSuite(t)
	interactions, err := Interactions()
	if err != nil {
		t.Fatal(err)
	}
	keys, err := ExtensionKeys()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(DataDir)
	if err != nil {
		t.Fatal(err)
	}
	prev, err := LoadCoverage(DataDir)
	if err != nil {
		t.Logf("not listing new entries: %s", strings.SplitN(err.Error(), "\n", 2)[0])
		prev = map[string]*Entry{}
	}

	if *update {
		for e, reason := range cfg.Exemptions.Exempt {
			if strings.TrimSpace(reason) == EntryTodo {
				delete(cfg.Exemptions.Exempt, e) // the backlog is derived
			}
		}
		r := Resolve(sources, suite, interactions, cfg, keys)
		for _, e := range append(r.Stale, r.Redundant...) {
			delete(cfg.Exemptions.Exempt, e)
		}
		_, statErr := os.Stat(filepath.Join(DataDir, RatchetsFile))
		cfg.Ratchets = ratchet(cfg.Ratchets, Resolve(sources, suite, interactions, cfg, keys), os.IsNotExist(statErr) || *raiseTodo)
		if err := cfg.Write(DataDir); err != nil {
			t.Fatal(err)
		}
		r = Resolve(sources, suite, interactions, cfg, keys)
		for _, e := range r.Errors {
			t.Error(e) // problems -update cannot fix (bad Roles, IDs, scope, exemption reasons)
		}
		checkViews(t, r)
		t.Logf("updated coverage/data/ (%d todo, maxTodo %d; maxRoleGaps %v)", len(r.Todo()), cfg.Ratchets.MaxTodo, cfg.Ratchets.MaxRoleGaps)
		return
	}

	r := Resolve(sources, suite, interactions, cfg, keys)
	errs := slices.Clone(r.Errors)
	errs = append(errs, checkViews(t, r)...)
	for _, e := range r.Stale {
		errs = append(errs, fmt.Sprintf("stale exemption %q: it no longer exists; remove it from coverage/data/%s (or run with -update)", e, ExemptionsFile))
	}
	for _, e := range r.Redundant {
		errs = append(errs, fmt.Sprintf("redundant exemption %q: covered by %v; remove it from coverage/data/%s (or run with -update)",
			e, r.Entries[e].CoveredBy, ExemptionsFile))
	}
	todo := r.Todo()
	if len(todo) > cfg.Ratchets.MaxTodo {
		added := newSince(prev, todo, func(e *Entry) bool { return e.Status == EntryTodo })
		errs = append(errs, fmt.Sprintf("%d todo entries exceed maxTodo=%d in coverage/data/%s: cover the new surface with a feature "+
			"(Covers), or exempt it with a reason in coverage/data/%s. New since %s: %s",
			len(todo), cfg.Ratchets.MaxTodo, RatchetsFile, ExemptionsFile, CoverageFile, abbreviate(added)))
	}
	gaps := r.Gaps()
	var gapStats []string
	for _, role := range r.ScopedRoles() {
		g := gaps[role]
		gapStats = append(gapStats, fmt.Sprintf("%s: %d", role, len(g)))
		if limit := cfg.Ratchets.MaxRoleGaps[role]; len(g) > limit {
			added := newSince(prev, g, func(e *Entry) bool { return slices.Contains(e.Gaps, role) })
			errs = append(errs, fmt.Sprintf("role %q leaves %d in-scope entries uncovered, above maxRoleGaps[%s]=%d in coverage/data/%s: "+
				"cover them with a feature that lists role %q, or narrow coverage/data/%s with a reason. New since %s: %s",
				role, len(g), role, limit, RatchetsFile, role, ScopeFile, CoverageFile, abbreviate(added)))
		}
	}
	for _, e := range errs {
		t.Error(e)
	}
	s := r.Summary()
	t.Logf("proxy conformance coverage: %d surface entries %v by %d features", s.Surface.Total, s.Surface.ByStatus, s.Features.Total)
	t.Logf("per-role gaps (in-scope entries the role does not cover): %s", strings.Join(gapStats, "; "))
}

// ratchet returns the ratchets for the resolution: lowered where the backlog shrank, raised only if
// raise is set. A role that is newly in scope starts at its current gaps.
func ratchet(old Ratchets, r *Resolution, raise bool) Ratchets {
	out := Ratchets{MaxTodo: old.MaxTodo, MaxRoleGaps: map[string]int{}}
	if n := len(r.Todo()); n < out.MaxTodo || raise {
		out.MaxTodo = n
	}
	for role, g := range r.Gaps() {
		prev, ok := old.MaxRoleGaps[role]
		if !ok || len(g) < prev || raise {
			prev = len(g)
		}
		out.MaxRoleGaps[role] = prev
	}
	return out
}

// newSince returns the entries that the previous coverage.yaml did not have in the same state (was),
// so a ratchet failure names what changed rather than the whole backlog.
func newSince(prev map[string]*Entry, entries []string, was func(*Entry) bool) []string {
	return slices.Filter(entries, func(entry string) bool {
		e, ok := prev[entry]
		return !ok || !was(e)
	})
}

func abbreviate(list []string) string {
	const limit = 20
	if len(list) == 0 {
		return "(none: run -update to refresh coverage.yaml, then compare)"
	}
	if len(list) > limit {
		return fmt.Sprintf("%s, ... (%d more)", strings.Join(list[:limit], ", "), len(list)-limit)
	}
	return strings.Join(list, ", ")
}

// checkViews keeps the generated views in sync: catalog.yaml (the features, for readers and tools
// that can't parse Go: docs, dashboards, other implementations' CI), surface.yaml (every entry and
// its source, so an API change shows up in review), coverage.yaml (every entry's status, scope,
// covering features and gaps, so a coverage change shows up in review) and summary.yaml (the counts
// the docs refer to). With -update it rewrites them.
func checkViews(t *testing.T, r *Resolution) []string {
	t.Helper()
	catalog, err := renderCatalog(r.Suite.Features)
	if err != nil {
		t.Fatal(err)
	}
	surface, err := r.RenderSurface()
	if err != nil {
		t.Fatal(err)
	}
	cov, err := r.RenderCoverage()
	if err != nil {
		t.Fatal(err)
	}
	summary, err := r.RenderSummary()
	if err != nil {
		t.Fatal(err)
	}
	var errs []string
	errs = append(errs, checkGenerated(t, catalogFile, "the features", catalog)...)
	errs = append(errs, checkGenerated(t, filepath.Join(DataDir, SurfaceFile), "the API surface", surface)...)
	errs = append(errs, checkGenerated(t, filepath.Join(DataDir, CoverageFile), "the surface, features and decisions", cov)...)
	errs = append(errs, checkGenerated(t, filepath.Join(DataDir, SummaryFile), "the API surface", summary)...)
	return errs
}

// catalogEntry is one feature in catalog.yaml. It is encoded with yaml.v3 rather than
// sigs.k8s.io/yaml so the fields keep this order (id first) for human readers.
type catalogEntry struct {
	ID          string   `yaml:"id"`
	Description string   `yaml:"description"`
	Roles       []string `yaml:"roles"`
	Profile     string   `yaml:"profile"`
	RequiresL7  bool     `yaml:"requiresL7"`
	// Security marks features held to the fail-open rule (IsSecurity).
	Security bool     `yaml:"security"`
	Covers   []string `yaml:"covers,omitempty"`
	Aliases  []string `yaml:"aliases,omitempty"`
	Cases    []string `yaml:"cases,omitempty"`
	// NoControl is false, or the reason the feature has no negative control.
	NoControl any `yaml:"noControl"`
}

const catalogHeader = `# Generated by TestCoverage from the Feature literals in features_*_test.go. DO NOT EDIT.
# Regenerate: go test ./tests/conformance/proxy/coverage/ -run TestCoverage -update
`

// renderCatalog returns the catalog for the features, sorted by ID.
func renderCatalog(features []Feature) ([]byte, error) {
	entries := make([]catalogEntry, 0, len(features))
	for _, f := range features {
		e := catalogEntry{
			ID: f.ID, Description: f.Description, Roles: f.Roles, Profile: f.Profile, RequiresL7: f.RequiresL7,
			Security: f.Security, Covers: f.Covers, Aliases: f.Aliases, Cases: f.Cases, NoControl: false,
		}
		if f.NoControl != "" {
			e.NoControl = f.NoControl
		}
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	var buf bytes.Buffer
	buf.WriteString(catalogHeader)
	if err := encodeYAML(&buf, entries); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// checkGenerated compares a generated file with its checked-in copy. With -update it rewrites the file.
func checkGenerated(t *testing.T, file, from string, want []byte) []string {
	t.Helper()
	if *update {
		if err := os.WriteFile(file, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	got, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		return []string{fmt.Sprintf("%s is out of date with %s: run go test ./tests/conformance/proxy/coverage/ -run TestCoverage -update", file, from)}
	}
	return nil
}

// TestResolve pins how an entry's status, scope and gaps follow from features and config.
func TestResolve(t *testing.T) {
	sources := map[string]string{
		"A.covered": SourceProto, "A.exempt": SourceProto, "A.both": SourceProto, "A.todo": SourceProto,
		"B.sub.x": SourceProto, "B.sub.y": SourceProto, "C.scoped": SourceProto,
	}
	suite := &Suite{
		Roles: []string{"ingress", "sidecar"},
		Features: []Feature{
			{ID: "f.ingress", Profile: ProfileCore, Roles: []string{"ingress"}, Covers: []string{"A.covered", "A.both", "B.sub.x", "C.scoped"}},
			{ID: "f.sidecar", Profile: ProfileCore, Roles: []string{"sidecar"}, Covers: []string{"A.covered"}},
		},
	}
	cfg := Config{
		Exemptions: Exemptions{Exempt: map[string]string{"A.exempt": "why", "A.both": "why", "B.sub.*": "why", "A.gone": "why"}},
		Scope:      Scope{Default: []string{"ingress"}, RoleScope: map[string][]string{"C": {"ingress", "sidecar"}}},
		Ratchets:   Ratchets{MaxRoleGaps: map[string]int{}},
	}
	r := Resolve(sources, suite, nil, cfg, nil)
	if len(r.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", r.Errors)
	}
	want := map[string]string{
		"A.covered": EntryCovered + " scope=[ingress] gaps=[]",
		"A.exempt":  EntryExempt + " scope=[] gaps=[]",
		"A.both":    EntryExempt + " scope=[] gaps=[]", // an explicit exemption wins, and is redundant
		"A.todo":    EntryTodo + " scope=[ingress] gaps=[ingress]",
		"B.sub.x":   EntryCovered + " scope=[] gaps=[]", // covered, but under a "*" exemption: out of scope
		"B.sub.y":   EntryExempt + " scope=[] gaps=[]",
		"C.scoped":  EntryCovered + " scope=[ingress sidecar] gaps=[sidecar]",
	}
	for entry, w := range want {
		e := r.Entries[entry]
		if got := fmt.Sprintf("%s scope=%v gaps=%v", e.Status, []string(e.Scope), []string(e.Gaps)); got != w {
			t.Errorf("%s: got %s, want %s", entry, got, w)
		}
	}
	if fmt.Sprint(r.Redundant) != "[A.both]" || fmt.Sprint(r.Stale) != "[A.gone]" {
		t.Errorf("redundant %v, stale %v; want [A.both], [A.gone]", r.Redundant, r.Stale)
	}
	r.Entries["B.sub.y"].ExemptBy = "B.sub.*"
	assertRoundTrip(t, r)

	cfg.Exemptions.Exempt = map[string]string{"A.todo": "todo"}
	cfg.Scope.Default = nil
	r = Resolve(sources, suite, nil, cfg, nil)
	if got := strings.Join(r.Errors, "; "); !strings.Contains(got, `"A.todo" is "todo"`) || !strings.Contains(got, "default lists no roles") {
		t.Errorf("want errors for a todo exemption and an empty default scope, got %q", got)
	}
}

// assertRoundTrip checks that LoadCoverage reads back what RenderSurface and RenderCoverage wrote.
func assertRoundTrip(t *testing.T, r *Resolution) {
	t.Helper()
	dir := t.TempDir()
	render := func(r *Resolution) (surface, cov []byte) {
		surface, err := r.RenderSurface()
		if err != nil {
			t.Fatal(err)
		}
		cov, err = r.RenderCoverage()
		if err != nil {
			t.Fatal(err)
		}
		return surface, cov
	}
	surface, cov := render(r)
	if err := os.WriteFile(filepath.Join(dir, SurfaceFile), surface, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, CoverageFile), cov, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCoverage(dir)
	if err != nil {
		t.Fatal(err)
	}
	gotSurface, gotCov := render(&Resolution{Entries: loaded})
	if !bytes.Equal(gotSurface, surface) || !bytes.Equal(gotCov, cov) {
		t.Errorf("LoadCoverage does not round-trip:\n%s\n%s\nwant:\n%s\n%s", gotSurface, gotCov, surface, cov)
	}
}

// TestSetIn pins how an applied spec maps to surface keys, and that every key it produces is one the
// surface walk produces too (otherwise a feature's claim could never be confirmed).
func TestSetIn(t *testing.T) {
	spec := map[string]any{
		"trafficPolicy": map[string]any{
			"tls":            map[string]any{"mode": "SIMPLE"},
			"connectionPool": map[string]any{"http": map[string]any{"http1MaxPendingRequests": 1}},
		},
		"subsets": []any{map[string]any{"name": "v1", "labels": map[string]any{"version": "v1"}}},
	}
	keys, ok := SetIn("DestinationRule", spec)
	if !ok {
		t.Fatal("DestinationRule is not an Istio kind")
	}
	for _, k := range []string{
		"DestinationRule.trafficPolicy.tls", "DestinationRule.trafficPolicy.tls.mode=SIMPLE",
		"DestinationRule.trafficPolicy.connectionPool.http.http1MaxPendingRequests", "DestinationRule.subsets.labels",
	} {
		if !keys[k] {
			t.Errorf("%s not set; got %v", k, slices.Sort(maps.Keys(keys)))
		}
	}
	if keys["DestinationRule.host"] || keys["DestinationRule.trafficPolicy.loadBalancer"] {
		t.Errorf("unset fields reported as set: %v", slices.Sort(maps.Keys(keys)))
	}

	keys, _ = SetIn("AuthorizationPolicy", map[string]any{"rules": []any{map[string]any{}}})
	if !keys["AuthorizationPolicy.action=ALLOW"] {
		t.Errorf("absent action should count as its meaningful zero value ALLOW; got %v", slices.Sort(maps.Keys(keys)))
	}

	surface := map[string]bool{}
	for _, k := range Surface() {
		surface[k] = true
	}
	for k := range keys {
		if !surface[k] {
			t.Errorf("SetIn produced %q, which is not a surface key", k)
		}
	}
	if _, ok := SetIn("HTTPRoute", nil); ok {
		t.Error("HTTPRoute is not an Istio kind")
	}
}

// TestField checks that every proto surface key resolves to its descriptor, so tools can document it.
func TestField(t *testing.T) {
	for k, src := range SurfaceSources() {
		if src != SourceProto {
			continue
		}
		fd, ev, ok := Field(k)
		if !ok || fd == nil || (strings.Contains(k, "=") && ev == nil) {
			t.Errorf("Field(%q) does not resolve", k)
		}
	}
	if _, _, ok := Field("flag:istiod:PILOT_HTTP10"); ok {
		t.Error("flags are not proto fields")
	}
}

// TestNoSleepsInFeatures forbids time.Sleep in features. A sleep guesses how long config takes to
// propagate: too short and the feature flakes, too long and the suite crawls, and either way a
// negative check after a sleep can pass before the config has arrived. Features must detect
// propagation with a witness instead (Gateway.WitnessTelemetry, or ExpectStable after a positive
// observation; see witness_test.go). Harness files may sleep: they implement those primitives.
func TestNoSleepsInFeatures(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(suiteDir, "features_*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no feature files found; is suiteDir correct?")
	}
	fset := token.NewFileSet()
	for _, f := range files {
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		timePkg := ""
		for _, imp := range af.Imports {
			if strings.Trim(imp.Path.Value, `"`) == "time" {
				timePkg = "time"
				if imp.Name != nil {
					timePkg = imp.Name.Name
				}
			}
		}
		if timePkg == "" {
			continue
		}
		ast.Inspect(af, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Sleep" {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == timePkg {
				t.Errorf("%s: time.Sleep in a feature: detect config propagation with a witness "+
					"(Gateway.WitnessTelemetry, or ExpectStable after a positive observation; see witness_test.go), not a sleep",
					fset.Position(sel.Pos()))
			}
			return true
		})
	}
}

// featureFiles maps each features file to the area(s) (first ID segment) of the features it holds,
// so a feature for an area lives in one predictable file. A new area needs a new entry here.
var featureFiles = map[string][]string{
	"features_backends_test.go":         {"backend"},
	"features_dataplane_test.go":        {"dataplane"},
	"features_defaults_test.go":         {"dataplane", "protocol"},
	"features_destinationrule_test.go":  {"backend"},
	"features_egress_test.go":           {"egress"},
	"features_gatewaytls_test.go":       {"istioapi"},
	"features_infra_test.go":            {"infra", "status"},
	"features_istioapi_test.go":         {"istioapi", "impl-specific"},
	"features_listeners_test.go":        {"listener", "protocol"},
	"features_meshconfig_test.go":       {"meshconfig", "flag"},
	"features_meshwide_test.go":         {"meshconfig", "proxyconfig"},
	"features_proxyconfig_test.go":      {"proxyconfig"},
	"features_routing_test.go":          {"istioapi", "backend", "flag"},
	"features_security_test.go":         {"security"},
	"features_security_ingress_test.go": {"security", "telemetry", "meshconfig", "flag", "dataplane"},
	"features_sidecar_test.go":          {"sidecar"},
	"features_telemetry_test.go":        {"telemetry"},
	"features_trafficpolicy_test.go":    {"backend"},
	"features_virtualservice_test.go":   {"istioapi"},
	"features_workload_test.go":         {"mesh"},
}

// TestFeatureFiles keeps features organized by area: every feature must be declared in a file listed
// in featureFiles whose areas include the feature's ID prefix.
func TestFeatureFiles(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(suiteDir, "features_*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if _, ok := featureFiles[filepath.Base(f)]; !ok {
			t.Errorf("%s is not listed in featureFiles: add it with the areas (ID prefixes) it holds", filepath.Base(f))
		}
	}
	for _, f := range readSuite(t).Features {
		file := filepath.Base(f.File)
		areas, ok := featureFiles[file]
		if !ok {
			t.Errorf("%s: feature %q is declared outside a features_*_test.go file listed in featureFiles", f.Pos, f.ID)
			continue
		}
		area, _, _ := strings.Cut(f.ID, ".")
		if !slices.Contains(areas, area) {
			t.Errorf("%s: feature %q (area %q) belongs in a file for that area, not %s (areas %v)", f.Pos, f.ID, area, file, areas)
		}
	}
}

// TestExtensionKeysTracked catches Istio-specific keys (annotations, tls options) introduced in the
// Gateway API translators without being added to the surface. Every such string literal must
// appear in a surface entry, or be exempted as "key:<key>".
func TestExtensionKeysTracked(t *testing.T) {
	surface := strings.Join(Surface(), "\n")
	cfg, err := LoadConfig(DataDir)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := ExtensionKeys()
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for k := range keys {
		if strings.Contains(surface, k) {
			continue
		}
		if _, ok := cfg.Exemptions.Exempt["key:"+k]; ok {
			continue
		}
		missing = append(missing, k)
	}
	sort.Strings(missing)
	for _, k := range missing {
		t.Errorf("Istio-specific key %q is used by a Gateway API translator but not tracked: add it to "+
			"gatewayext.Registry() in pilot/pkg/config/kube/gatewayext (and cover it with a feature), or exempt it as \"key:%s\"", k, k)
	}
}

// TestCheckInteractions pins the validation of interactions.yaml entries and how coveredBy resolves
// feature aliases, without the real inventory.
func TestCheckInteractions(t *testing.T) {
	features := []Feature{{ID: "security.authz.deny-overrides-allow", Aliases: []string{"security.authz.deny-wins"}}}
	roles := map[string]bool{"ingress": true, "sidecar": true}
	valid := Interaction{ID: "authz.deny-before-allow", Description: "d", Roles: []string{"ingress"}, Source: "https://istio.io/latest/docs/"}
	with := func(f func(i *Interaction)) []Interaction {
		i := valid
		f(&i)
		return []Interaction{i}
	}
	cases := []struct {
		name        string
		in          []Interaction
		wantErr     string // substring; "" means valid
		wantCovered []string
	}{
		{"valid, uncovered", with(func(*Interaction) {}), "", nil},
		{
			"coveredBy alias resolves to the live ID",
			with(func(i *Interaction) { i.CoveredBy = []string{"security.authz.deny-wins"} }),
			"",
			[]string{"security.authz.deny-overrides-allow"},
		},
		{"coveredBy unknown feature", with(func(i *Interaction) { i.CoveredBy = []string{"security.nope"} }), `unknown feature "security.nope"`, nil},
		{"repository source", with(func(i *Interaction) { i.Source = "pilot/pkg/model/destination_rule.go" }), "", nil},
		{"missing repository source", with(func(i *Interaction) { i.Source = "pilot/pkg/nope.go" }), "does not exist", nil},
		{"foreign URL", with(func(i *Interaction) { i.Source = "https://example.com/" }), "must be an https://istio.io/ page", nil},
		{"no source", with(func(i *Interaction) { i.Source = "" }), "needs a source", nil},
		{"unknown role", with(func(i *Interaction) { i.Roles = []string{"mesh"} }), `unknown role "mesh"`, nil},
		{"no roles", with(func(i *Interaction) { i.Roles = nil }), "lists no roles", nil},
		{"no description", with(func(i *Interaction) { i.Description = " " }), "needs a description", nil},
		{"invalid id", with(func(i *Interaction) { i.ID = "Authz Order" }), "invalid id", nil},
		{"duplicate id", []Interaction{valid, valid}, "duplicate id", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs, covered := checkInteractions(tc.in, features, roles)
			got := strings.Join(errs, "; ")
			switch {
			case tc.wantErr == "" && got != "":
				t.Fatalf("want valid, got %q", got)
			case tc.wantErr != "" && !strings.Contains(got, tc.wantErr):
				t.Fatalf("want error containing %q, got %q", tc.wantErr, got)
			}
			if g := covered["interaction:"+valid.ID]; strings.Join(g, ",") != strings.Join(tc.wantCovered, ",") {
				t.Fatalf("coveredBy resolved to %v, want %v", g, tc.wantCovered)
			}
		})
	}
}
