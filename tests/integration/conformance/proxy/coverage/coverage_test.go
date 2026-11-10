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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	yamlv3 "gopkg.in/yaml.v3"
	"sigs.k8s.io/yaml"

	"istio.io/istio/pkg/test/env"
)

var update = flag.Bool("update", false,
	"Rewrite exemptions.yaml: drop stale/covered entries and add uncovered surface as 'todo'. "+
		"maxTodo is never raised, so new 'todo' entries still fail the check.")

var raiseTodo = flag.Bool("raise-todo", false,
	"With -update, allow maxTodo to increase. Only for deliberate surface expansions (e.g. tracking a new "+
		"API); never to silence new fields. Reviewers should see this as a maxTodo increase in the diff.")

const (
	suiteDir       = ".."
	exemptionsFile = "exemptions.yaml"
	todo           = "todo"
)

// catalogFile is the generated feature catalog, next to the features it describes.
var catalogFile = filepath.Join(suiteDir, "catalog.yaml")

// Exemptions is the checked-in exemptions.yaml.
type Exemptions struct {
	// MaxTodo is a ratchet: the number of "todo" exemptions may only go down. New surface must be
	// covered by a feature or exempted with a real reason.
	MaxTodo int `json:"maxTodo"`
	// Exempt maps a surface entry (or "key:<string>", see TestExtensionKeysTracked) to the reason it
	// is not covered by a conformance feature.
	Exempt map[string]string `json:"exempt"`
	// RoleScope maps a surface prefix ("ServiceEntry", "AuthorizationPolicy.action=DENY") to the roles
	// that must each cover the matching entries with a feature of their own. Without it coverage is a
	// union across roles, so a field covered by an ingress feature counts as covered for a sidecar
	// that was never tested with it. The longest matching prefix wins, so a narrower entry can widen
	// or narrow the role list of a subtree. Entries exempted with a real reason are out of scope for
	// every role; "todo" entries are in scope.
	RoleScope map[string][]string `json:"roleScope,omitempty"`
	// MaxRoleGaps is a per-role ratchet on in-scope entries the role does not cover. -update lowers
	// it; only -raise-todo raises it.
	MaxRoleGaps map[string]int `json:"maxRoleGaps,omitempty"`
}

// declaredFeature is a Feature literal passed to Register, read from the suite's source.
type declaredFeature struct {
	ID          string
	Description string
	Roles       []string
	Profile     string
	RequiresL7  bool
	Covers      []string
	Aliases     []string
	NoControl   string
	// Cases are the names of the subcases the feature's Run declares with s.Case(t, "<name>", ...).
	Cases []string
	Pos   string
}

// Feature profiles (Feature.Profile in the suite). An unset profile means core, except for
// impl-specific.* features, which are not conformance requirements by definition.
const (
	profileCore         = "core"
	profileExtended     = "extended"
	profileImplSpecific = "impl-specific"
	implSpecificPrefix  = "impl-specific."
)

// profileConsts maps the suite's Profile constants to their values, so features may use either.
var profileConsts = map[string]string{
	"ProfileCore":         profileCore,
	"ProfileExtended":     profileExtended,
	"ProfileImplSpecific": profileImplSpecific,
}

// TestCoverage verifies that every piece of Istio API surface relevant to a proxy role is either
// exercised by a proxy conformance feature (Feature.Covers) or explicitly exempted. It fails when:
//   - a new field/enum value/annotation appears (e.g. an istio/api bump) and nobody decided about it
//   - a feature declares coverage of something that doesn't exist (typo or removed field)
//   - an exemption is stale or redundant
//   - the number of "todo" exemptions grows
func TestCoverage(t *testing.T) {
	surface := Surface()
	surfaceSet := map[string]bool{}
	for _, s := range surface {
		surfaceSet[s] = true
	}
	features, roles := readFeatures(t)
	ex := readExemptions(t)

	var errs []string
	// Every feature must name the roles it applies to. Roles is required at runtime (Register
	// panics), but that only runs with the integration build tag; this catches it in `make test`.
	knownRoles := map[string]bool{}
	for _, r := range roles {
		knownRoles[r] = true
	}
	for _, f := range features {
		if len(f.Roles) == 0 {
			errs = append(errs, fmt.Sprintf("%s: feature %q lists no Roles: list every role the behavior applies to", f.Pos, f.ID))
		}
		for _, r := range f.Roles {
			if !knownRoles[r] {
				errs = append(errs, fmt.Sprintf("%s: feature %q lists unknown role %q", f.Pos, f.ID, r))
			}
		}
	}

	errs = append(errs, featureIDErrors(features)...)
	errs = append(errs, checkCatalog(t, features)...)

	covered := map[string][]string{}
	for _, f := range features {
		for _, c := range f.Covers {
			if !surfaceSet[c] {
				errs = append(errs, fmt.Sprintf("%s: feature %q covers %q, which is not part of the API surface (typo, or removed field?)", f.Pos, f.ID, c))
				continue
			}
			covered[c] = append(covered[c], f.ID)
		}
	}
	interactions, err := Interactions()
	if err != nil {
		t.Fatal(err)
	}
	ierrs, coveredBy := checkInteractions(interactions, features, knownRoles)
	errs = append(errs, ierrs...)
	for c, ids := range coveredBy {
		covered[c] = append(covered[c], ids...)
	}

	// Per-role accounting. Coverage is still a global union (see the known limitation in
	// architecture/tests/proxy-conformance.md §3.5), but reporting it per role makes a role whose
	// suite is thin (egress was two features when it was split out) visible instead of hidden in
	// the total.
	roleFeatures := map[string]int{}
	roleCovered := map[string]map[string]bool{}
	for _, f := range features {
		for _, r := range f.Roles {
			roleFeatures[r]++
			if roleCovered[r] == nil {
				roleCovered[r] = map[string]bool{}
			}
			for _, c := range f.Covers {
				if surfaceSet[c] {
					roleCovered[r][c] = true
				}
			}
		}
	}
	// An interaction coveredBy a feature counts for that feature's roles, as if it were in Covers.
	for _, f := range features {
		for c, ids := range coveredBy {
			if !contains(ids, f.ID) {
				continue
			}
			for _, r := range f.Roles {
				if roleCovered[r] == nil {
					roleCovered[r] = map[string]bool{}
				}
				roleCovered[r][c] = true
			}
		}
	}
	var roleStats []string
	for _, r := range roles {
		roleStats = append(roleStats, fmt.Sprintf("%s: %d features, %d entries", r, roleFeatures[r], len(roleCovered[r])))
		if roleFeatures[r] == 0 {
			errs = append(errs, fmt.Sprintf("role %q has no conformance features: every declared role must be exercised", r))
		}
	}

	var uncovered, stale, redundant []string
	prefixes := ex.prefixes()
	for _, s := range surface {
		_, exempt := ex.Exempt[s]
		if !exempt && prefixes.match(s) != "" {
			continue // exempted as part of a subtree; covering parts of it is fine
		}
		switch {
		case len(covered[s]) > 0 && exempt:
			redundant = append(redundant, s)
		case len(covered[s]) == 0 && !exempt:
			uncovered = append(uncovered, s)
		}
	}
	keys := extensionKeys(t)
	for e := range ex.Exempt {
		if strings.HasPrefix(e, "key:") {
			if !keys[strings.TrimPrefix(e, "key:")] {
				stale = append(stale, e)
			}
			continue
		}
		if strings.HasSuffix(e, "*") {
			if !anyHasPrefix(surface, strings.TrimSuffix(e, "*")) {
				stale = append(stale, e)
			}
			continue
		}
		if !surfaceSet[e] {
			stale = append(stale, e)
		}
	}
	sort.Strings(stale)

	todos := 0
	for _, r := range ex.Exempt {
		if r == todo {
			todos++
		}
	}

	scopeErrs, gaps := roleGaps(surface, roles, roleCovered, ex, interactionScope(interactions))
	errs = append(errs, scopeErrs...)

	if *update {
		for r, g := range gaps {
			if prev, ok := ex.MaxRoleGaps[r]; !ok || len(g) < prev || *raiseTodo {
				if ex.MaxRoleGaps == nil {
					ex.MaxRoleGaps = map[string]int{}
				}
				ex.MaxRoleGaps[r] = len(g)
			}
		}
		for r := range ex.MaxRoleGaps {
			if _, ok := gaps[r]; !ok {
				delete(ex.MaxRoleGaps, r) // role no longer scoped
			}
		}
		for _, e := range errs {
			t.Error(e) // problems -update cannot fix (bad Roles, IDs, scope)
		}
		for _, s := range append(stale, redundant...) {
			delete(ex.Exempt, s)
		}
		for _, s := range uncovered {
			ex.Exempt[s] = todo
		}
		n := 0
		for _, r := range ex.Exempt {
			if r == todo {
				n++
			}
		}
		if ex.MaxTodo == 0 || n < ex.MaxTodo || *raiseTodo {
			ex.MaxTodo = n
		}
		writeExemptions(t, ex)
		t.Logf("updated %s (%d todo, maxTodo %d)", exemptionsFile, n, ex.MaxTodo)
		return
	}

	for _, s := range uncovered {
		errs = append(errs, fmt.Sprintf("uncovered API surface %q: add a feature with Covers: %q, or an exemption with a reason in coverage/%s", s, s, exemptionsFile))
	}
	for _, s := range stale {
		errs = append(errs, fmt.Sprintf("stale exemption %q: it no longer exists; remove it (or run with -update)", s))
	}
	for _, s := range redundant {
		errs = append(errs, fmt.Sprintf("redundant exemption %q: covered by %v; remove it (or run with -update)", s, covered[s]))
	}
	if todos > ex.MaxTodo {
		errs = append(errs, fmt.Sprintf("%d 'todo' exemptions exceed maxTodo=%d: new API surface needs a conformance feature or a real reason", todos, ex.MaxTodo))
	}
	var gapStats []string
	for _, r := range roles {
		g, ok := gaps[r]
		if !ok {
			continue
		}
		gapStats = append(gapStats, fmt.Sprintf("%s: %d", r, len(g)))
		if max := ex.MaxRoleGaps[r]; len(g) > max {
			errs = append(errs, fmt.Sprintf("role %q leaves %d in-scope surface entries uncovered, above maxRoleGaps[%s]=%d: "+
				"cover the new entries with a feature that lists role %q, or narrow roleScope in coverage/%s with a reason",
				r, len(g), r, max, r, exemptionsFile))
		}
	}
	for _, e := range errs {
		t.Error(e)
	}
	t.Logf("proxy conformance coverage: %d/%d surface entries covered by %d features, %d exempt (%d todo)",
		len(covered), len(surface), len(features), len(ex.Exempt), todos)
	t.Logf("per role: %s", strings.Join(roleStats, "; "))
	t.Logf("per-role gaps (roleScope entries the role does not cover): %s", strings.Join(gapStats, "; "))
	for _, r := range roles {
		if len(gaps[r]) > 0 {
			t.Logf("  %s gaps: %s", r, strings.Join(gaps[r], ", "))
		}
	}
}

// featureIDErrors checks that feature IDs and aliases are globally unique and that no alias names a
// live feature. Aliases keep references to a renamed feature (release notes, commit trailers,
// declared manifests) resolvable, so an alias must point to exactly one feature.
func featureIDErrors(features []declaredFeature) []string {
	var errs []string
	ids := map[string]string{}
	for _, f := range features {
		if prev, ok := ids[f.ID]; ok {
			errs = append(errs, fmt.Sprintf("%s: duplicate feature ID %q (also at %s)", f.Pos, f.ID, prev))
		}
		ids[f.ID] = f.Pos
		if strings.HasPrefix(f.ID, implSpecificPrefix) && f.Profile != profileImplSpecific {
			errs = append(errs, fmt.Sprintf("%s: feature %q is impl-specific by ID but declares Profile %q", f.Pos, f.ID, f.Profile))
		}
	}
	aliases := map[string]string{}
	for _, f := range features {
		for _, a := range f.Aliases {
			if _, ok := ids[a]; ok {
				errs = append(errs, fmt.Sprintf("%s: alias %q of feature %q is a live feature ID", f.Pos, a, f.ID))
			}
			if prev, ok := aliases[a]; ok {
				errs = append(errs, fmt.Sprintf("%s: alias %q of feature %q is also an alias of %q", f.Pos, a, f.ID, prev))
			}
			aliases[a] = f.ID
		}
	}
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
	Covers      []string `yaml:"covers,omitempty"`
	Aliases     []string `yaml:"aliases,omitempty"`
	Cases       []string `yaml:"cases,omitempty"`
	// NoControl is false, or the reason the feature has no negative control.
	NoControl any `yaml:"noControl"`
}

const catalogHeader = `# Generated by TestCoverage from the Feature literals in features_*_test.go. DO NOT EDIT.
# Regenerate: go test ./tests/integration/conformance/proxy/coverage/ -run TestCoverage -update
`

// renderCatalog returns the catalog for the features, sorted by ID.
func renderCatalog(features []declaredFeature) ([]byte, error) {
	entries := make([]catalogEntry, 0, len(features))
	for _, f := range features {
		e := catalogEntry{
			ID: f.ID, Description: f.Description, Roles: f.Roles, Profile: f.Profile,
			RequiresL7: f.RequiresL7, Covers: f.Covers, Aliases: f.Aliases, Cases: f.Cases, NoControl: false,
		}
		if f.NoControl != "" {
			e.NoControl = f.NoControl
		}
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	var buf bytes.Buffer
	buf.WriteString(catalogHeader)
	enc := yamlv3.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(entries); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// checkCatalog keeps catalog.yaml, the suite's feature list for readers and tools that can't parse
// Go (docs, dashboards, other implementations' CI), in sync with the source. With -update it
// rewrites the file.
func checkCatalog(t *testing.T, features []declaredFeature) []string {
	t.Helper()
	want, err := renderCatalog(features)
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile(catalogFile, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	got, err := os.ReadFile(catalogFile)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		return []string{fmt.Sprintf("%s is out of date with the features: run go test ./tests/integration/conformance/proxy/coverage/ -run TestCoverage -update", catalogFile)}
	}
	return nil
}

// roleGaps returns, per scoped role, the sorted in-scope surface entries that no feature of that
// role covers, and errors for invalid roleScope entries. implicit holds roleScope entries derived
// from other sources (interactions.yaml); they are not written back to exemptions.yaml and an
// explicit roleScope entry for the same key wins.
func roleGaps(surface, roles []string, roleCovered map[string]map[string]bool, ex Exemptions, implicit map[string][]string) ([]string, map[string][]string) {
	var errs []string
	known := map[string]bool{}
	for _, r := range roles {
		known[r] = true
	}
	scoped := map[string]bool{}
	for pre, rs := range ex.RoleScope {
		if !anyInScope(surface, pre) {
			errs = append(errs, fmt.Sprintf("roleScope prefix %q matches no surface entry: fix or remove it in coverage/%s", pre, exemptionsFile))
		}
		for _, r := range rs {
			if !known[r] {
				errs = append(errs, fmt.Sprintf("roleScope prefix %q lists unknown role %q", pre, r))
			}
			scoped[r] = true
		}
	}
	for r := range ex.MaxRoleGaps {
		if !known[r] {
			errs = append(errs, fmt.Sprintf("maxRoleGaps lists unknown role %q", r))
		}
	}
	sort.Strings(errs)
	scope := map[string][]string{}
	for pre, rs := range implicit {
		scope[pre] = rs
		for _, r := range rs {
			scoped[r] = true
		}
	}
	for pre, rs := range ex.RoleScope {
		scope[pre] = rs
	}

	gaps := map[string][]string{}
	for r := range scoped {
		gaps[r] = []string{}
	}
	prefixes := ex.prefixes()
	for _, s := range surface {
		// Exempt with a real reason: not required for any role. A "todo" exemption is backlog and
		// still counts as a gap.
		if reason, ok := ex.Exempt[s]; ok {
			if reason != todo {
				continue
			}
		} else if p := prefixes.match(s); p != "" && ex.Exempt[p+"*"] != todo {
			continue
		}
		for _, r := range scope[scopePrefix(scope, s)] {
			if !roleCovered[r][s] {
				gaps[r] = append(gaps[r], s)
			}
		}
	}
	return errs, gaps
}

// scopePrefix returns the longest roleScope prefix matching the entry, or "".
func scopePrefix(scope map[string][]string, s string) string {
	best := ""
	for pre := range scope {
		if inScope(s, pre) && len(pre) > len(best) {
			best = pre
		}
	}
	return best
}

// inScope reports whether entry s is pre itself or below it (a field or enum value of pre). A
// trailing "*" makes pre a plain string prefix, e.g. "annotation:sidecar.istio.io/*".
func inScope(s, pre string) bool {
	if p, ok := strings.CutSuffix(pre, "*"); ok {
		return strings.HasPrefix(s, p)
	}
	return s == pre || strings.HasPrefix(s, pre+".") || strings.HasPrefix(s, pre+"=")
}

// interactionScope returns the implicit roleScope of the interactions: each interaction must be
// covered by every non-ingress role it lists (ingress coverage is the global check).
func interactionScope(interactions []Interaction) map[string][]string {
	out := map[string][]string{}
	for _, i := range interactions {
		var rs []string
		for _, r := range i.Roles {
			if r != "ingress" {
				rs = append(rs, r)
			}
		}
		if len(rs) > 0 {
			out["interaction:"+i.ID] = rs
		}
	}
	return out
}

// interactionIDPattern keeps interaction IDs usable in Covers and surface names.
var interactionIDPattern = regexp.MustCompile(`^[a-z0-9]+([.-][a-z0-9]+)*$`)

// checkInteractions validates interactions.yaml and returns, per covered interaction surface entry,
// the live feature IDs its coveredBy names (aliases resolved).
func checkInteractions(interactions []Interaction, features []declaredFeature, knownRoles map[string]bool) ([]string, map[string][]string) {
	var errs []string
	ids := map[string]string{}
	for _, f := range features {
		ids[f.ID] = f.ID
		for _, a := range f.Aliases {
			ids[a] = f.ID
		}
	}
	seen := map[string]bool{}
	coveredBy := map[string][]string{}
	for _, i := range interactions {
		src := fmt.Sprintf("%s: interaction %q", InteractionsFile, i.ID)
		if !interactionIDPattern.MatchString(i.ID) {
			errs = append(errs, fmt.Sprintf("%s: invalid id (want lowercase words separated by '.' or '-')", src))
		}
		if seen[i.ID] {
			errs = append(errs, fmt.Sprintf("%s: duplicate id", src))
		}
		seen[i.ID] = true
		if strings.TrimSpace(i.Description) == "" {
			errs = append(errs, fmt.Sprintf("%s: needs a description", src))
		}
		if len(i.Roles) == 0 {
			errs = append(errs, fmt.Sprintf("%s: lists no roles", src))
		}
		for _, r := range i.Roles {
			if !knownRoles[r] {
				errs = append(errs, fmt.Sprintf("%s: unknown role %q", src, r))
			}
		}
		if err := checkSource(i.Source); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", src, err))
		}
		for _, ref := range i.CoveredBy {
			id, ok := ids[ref]
			if !ok {
				errs = append(errs, fmt.Sprintf("%s: coveredBy names unknown feature %q", src, ref))
				continue
			}
			coveredBy["interaction:"+i.ID] = append(coveredBy["interaction:"+i.ID], id)
		}
	}
	return errs, coveredBy
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

func anyInScope(surface []string, pre string) bool {
	for _, s := range surface {
		if inScope(s, pre) {
			return true
		}
	}
	return false
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

// istioKeyPattern matches Istio annotation/option keys used as string literals.
var istioKeyPattern = regexp.MustCompile(`^(gateway|networking|proxy|sidecar|security|telemetry)\.istio\.io/[A-Za-z0-9.-]+$`)

// translatorDirs are the Gateway API translators and deployment controllers for all gateway
// implementations.
var translatorDirs = []string{
	"pilot/pkg/config/kube/gateway",
	"pilot/pkg/config/kube/gatewaycommon",
	"pilot/pkg/config/kube/agentgateway",
}

// TestExtensionKeysTracked catches Istio-specific keys (annotations, tls options) introduced in the
// Gateway API translators without being added to the surface. Every such string literal must
// appear in a surface entry, or be exempted as "key:<key>".
func TestExtensionKeysTracked(t *testing.T) {
	surface := strings.Join(Surface(), "\n")
	ex := readExemptions(t)
	var missing []string
	for k := range extensionKeys(t) {
		if strings.Contains(surface, k) {
			continue
		}
		if _, ok := ex.Exempt["key:"+k]; ok {
			continue
		}
		missing = append(missing, k)
	}
	sort.Strings(missing)
	for _, k := range missing {
		t.Errorf("Istio-specific key %q is used by a Gateway API translator but not tracked: add it to "+
			"gatewayAPIExtensions in coverage/surface.go (and cover it with a feature), or exempt it as \"key:%s\"", k, k)
	}
}

func extensionKeys(t *testing.T) map[string]bool {
	t.Helper()
	keys := map[string]bool{}
	for _, d := range translatorDirs {
		dir := filepath.Join(env.IstioSrc, d)
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			af, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
			if err != nil {
				t.Fatal(err)
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
	return keys
}

// readFeatures parses the suite's *_test.go files (which carry the integ build tag and can't be
// imported) and extracts the Feature literals passed to Register, including the roles they apply
// to. It also returns the declared roles, in source order.
func readFeatures(t *testing.T) ([]declaredFeature, []string) {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob(filepath.Join(suiteDir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	roles := readRoleTable(t, files, fset)
	var out []declaredFeature
	for _, f := range files {
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if id, ok := cl.Type.(*ast.Ident); !ok || id.Name != "Feature" {
				return true
			}
			df := declaredFeature{Pos: fset.Position(cl.Pos()).String()}
			registered := false
			for _, e := range cl.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				switch kv.Key.(*ast.Ident).Name {
				case "ID":
					df.ID = stringLit(t, fset, kv.Value)
				case "Run":
					// Only features passed to Register have Run. Scenario literals such as
					// Feature{ID: "shared"} are placeholders and are not part of the suite.
					registered = true
					df.Cases = caseNames(kv.Value)
				case "Roles":
					df.Roles = roles.resolve(t, fset, kv.Value)
				case "Covers":
					df.Covers = stringList(t, fset, "Covers", kv.Value)
				case "Aliases":
					df.Aliases = stringList(t, fset, "Aliases", kv.Value)
				case "Description":
					df.Description = concatLit(t, fset, "Description", kv.Value)
				case "NoControl":
					df.NoControl = concatLit(t, fset, "NoControl", kv.Value)
				case "RequiresL7":
					id, ok := kv.Value.(*ast.Ident)
					if !ok || (id.Name != "true" && id.Name != "false") {
						t.Fatalf("%s: RequiresL7 must be true or false (it is read from source)", fset.Position(kv.Value.Pos()))
					}
					df.RequiresL7 = id.Name == "true"
				case "Profile":
					df.Profile = profileValue(t, fset, kv.Value)
				}
			}
			if df.ID != "" && registered {
				if df.Profile == "" {
					df.Profile = profileCore
					if strings.HasPrefix(df.ID, implSpecificPrefix) {
						df.Profile = profileImplSpecific
					}
				}
				out = append(out, df)
			}
			return true
		})
	}
	if len(out) == 0 {
		t.Fatal("no features found; is suiteDir correct?")
	}
	return out, roles.constNames()
}

// roleTable resolves the role expressions features use: the role constants
// (RoleIngress Role = "ingress") and package-level aliases (var ingressRole = []Role{RoleIngress}).
type roleTable struct {
	consts map[string]string
	vars   map[string][]string
	order  []string
}

func readRoleTable(t *testing.T, files []string, fset *token.FileSet) *roleTable {
	t.Helper()
	rt := &roleTable{consts: map[string]string{}, vars: map[string][]string{}}
	var asts []*ast.File
	for _, f := range files {
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		asts = append(asts, af)
	}
	// Constants first: vars such as ingressRole = []Role{RoleIngress} reference them, possibly from
	// a file parsed later.
	for _, af := range asts {
		rt.forEachValueSpec(af, func(vs *ast.ValueSpec, i int, name string) {
			if !isRoleType(vs.Type) {
				return
			}
			if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				rt.consts[name] = stringLit(t, fset, lit)
				rt.order = append(rt.order, name)
			}
		})
	}
	for _, af := range asts {
		rt.forEachValueSpec(af, func(vs *ast.ValueSpec, i int, name string) {
			if vs.Type != nil {
				return
			}
			if rs, ok := rt.resolveExpr(vs.Values[i]); ok {
				rt.vars[name] = rs
			}
		})
	}
	if len(rt.consts) == 0 {
		t.Fatal("no Role constants found; is suiteDir correct?")
	}
	return rt
}

// forEachValueSpec calls f for each name/value pair in a const or var declaration.
func (rt *roleTable) forEachValueSpec(af *ast.File, f func(vs *ast.ValueSpec, i int, name string)) {
	for _, d := range af.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) != len(vs.Names) {
				continue
			}
			for i, name := range vs.Names {
				f(vs, i, name.Name)
			}
		}
	}
}

func isRoleType(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "Role"
}

// resolveExpr reads a []Role composite literal or a role constant, without reporting errors. It is
// used while building the table, before vars are fully known.
func (rt *roleTable) resolveExpr(e ast.Expr) ([]string, bool) {
	switch v := e.(type) {
	case *ast.Ident:
		if r, ok := rt.consts[v.Name]; ok {
			return []string{r}, true
		}
		return rt.vars[v.Name], rt.vars[v.Name] != nil
	case *ast.CompositeLit:
		var out []string
		for _, el := range v.Elts {
			rs, ok := rt.resolveExpr(el)
			if !ok {
				return nil, false
			}
			out = append(out, rs...)
		}
		return out, len(out) > 0
	}
	return nil, false
}

// resolve reads a Feature's Roles expression and reports unknown identifiers.
func (rt *roleTable) resolve(t *testing.T, fset *token.FileSet, e ast.Expr) []string {
	t.Helper()
	if rs, ok := rt.resolveExpr(e); ok {
		return rs
	}
	switch v := e.(type) {
	case *ast.Ident:
		t.Errorf("%s: unknown role identifier %q (roles must use the Role constants)", fset.Position(v.Pos()), v.Name)
	default:
		t.Errorf("%s: Roles must be a []Role literal or a role alias", fset.Position(e.Pos()))
	}
	return nil
}

// constNames returns the role names in declaration order.
func (rt *roleTable) constNames() []string {
	out := make([]string, 0, len(rt.order))
	for _, n := range rt.order {
		out = append(out, rt.consts[n])
	}
	return out
}

func stringLit(t *testing.T, fset *token.FileSet, e ast.Expr) string {
	t.Helper()
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		t.Fatalf("%s: expected a string literal", fset.Position(e.Pos()))
	}
	v, err := strconv.Unquote(bl.Value)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// caseNames returns the literal names passed to s.Case(t, "<name>", fn) in a feature's Run, in
// source order. Case names are informational in the catalog, so a non-literal name is skipped rather
// than failing the check.
func caseNames(run ast.Expr) []string {
	var out []string
	seen := map[string]bool{}
	ast.Inspect(run, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 3 {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Case" {
			return true
		}
		if bl, ok := call.Args[1].(*ast.BasicLit); ok && bl.Kind == token.STRING {
			if v, err := strconv.Unquote(bl.Value); err == nil && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
		return true
	})
	return out
}

// stringList reads a []string literal of string literals.
func stringList(t *testing.T, fset *token.FileSet, field string, e ast.Expr) []string {
	t.Helper()
	list, ok := e.(*ast.CompositeLit)
	if !ok {
		t.Fatalf("%s: %s must be a []string literal (it is read from source)", fset.Position(e.Pos()), field)
	}
	var out []string
	for _, c := range list.Elts {
		out = append(out, stringLit(t, fset, c))
	}
	return out
}

// concatLit reads a string literal or a '+' concatenation of string literals, the forms used for
// long descriptions. Anything else (constants, fmt.Sprintf) can't be resolved from source and fails,
// because the catalog would silently lose the text.
func concatLit(t *testing.T, fset *token.FileSet, field string, e ast.Expr) string {
	t.Helper()
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			s, err := strconv.Unquote(v.Value)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}
	case *ast.BinaryExpr:
		if v.Op == token.ADD {
			return concatLit(t, fset, field, v.X) + concatLit(t, fset, field, v.Y)
		}
	case *ast.ParenExpr:
		return concatLit(t, fset, field, v.X)
	}
	t.Fatalf("%s: %s must be a string literal or a '+' concatenation of string literals (it is read from source for catalog.yaml)",
		fset.Position(e.Pos()), field)
	return ""
}

// profileValue reads a Feature's Profile: one of the Profile constants or a string literal holding a
// valid profile.
func profileValue(t *testing.T, fset *token.FileSet, e ast.Expr) string {
	t.Helper()
	var v string
	switch x := e.(type) {
	case *ast.Ident:
		p, ok := profileConsts[x.Name]
		if !ok {
			t.Fatalf("%s: unknown Profile %q (use ProfileCore, ProfileExtended or ProfileImplSpecific)", fset.Position(e.Pos()), x.Name)
		}
		return p
	case *ast.BasicLit:
		v = stringLit(t, fset, x)
	default:
		t.Fatalf("%s: Profile must be a Profile constant or a string literal (it is read from source)", fset.Position(e.Pos()))
	}
	for _, p := range profileConsts {
		if v == p {
			return v
		}
	}
	t.Fatalf("%s: invalid Profile %q (want core, extended or impl-specific)", fset.Position(e.Pos()), v)
	return ""
}

func readExemptions(t *testing.T) Exemptions {
	t.Helper()
	ex := Exemptions{Exempt: map[string]string{}}
	b, err := os.ReadFile(exemptionsFile)
	if os.IsNotExist(err) && *update {
		return ex
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(b, &ex); err != nil {
		t.Fatal(err)
	}
	if ex.Exempt == nil {
		ex.Exempt = map[string]string{}
	}
	return ex
}

func writeExemptions(t *testing.T, ex Exemptions) {
	t.Helper()
	b, err := yaml.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	header := `# API surface not (yet) covered by the proxy conformance suite, with the reason.
# Checked by TestCoverage (go test ./tests/integration/conformance/proxy/coverage/).
#
# - "todo" entries are the backlog. maxTodo is a ratchet: it may only go down, so new API surface
#   needs a conformance feature (Feature.Covers) or a real reason here.
# - "key:<string>" entries exempt Istio-specific keys found in the Gateway API translators.
# - Entries ending in "*" exempt every entry with that prefix: "<Kind>.<path>.*" a whole subtree
#   (e.g. sidecar-only MeshConfig sections), "annotation:<prefix>*" an annotation family.
# - roleScope keys match an entry and its fields; a key ending in "*" matches by plain prefix.
#   interactions.yaml adds implicit roleScope entries for interaction:<id> from each entry's roles.
# - Run with -update to drop stale/covered entries after adding features.
`
	if err := os.WriteFile(exemptionsFile, append([]byte(header), b...), 0o644); err != nil {
		t.Fatal(err)
	}
}

type prefixSet []string

// prefixes returns the prefix exemptions: entries ending in "*", e.g. a subtree ("Kind.path.*") or
// an annotation family ("annotation:readiness.status.sidecar.istio.io/*").
func (e Exemptions) prefixes() prefixSet {
	var out prefixSet
	for k := range e.Exempt {
		if strings.HasSuffix(k, "*") {
			out = append(out, strings.TrimSuffix(k, "*"))
		}
	}
	return out
}

func (p prefixSet) match(s string) string {
	for _, pre := range p {
		if strings.HasPrefix(s, pre) || s == strings.TrimSuffix(pre, ".") {
			return pre
		}
	}
	return ""
}

func anyHasPrefix(list []string, pre string) bool {
	for _, s := range list {
		if strings.HasPrefix(s, pre) {
			return true
		}
	}
	return false
}

// TestCheckInteractions pins the validation of interactions.yaml entries and how coveredBy resolves
// feature aliases, without the real inventory.
func TestCheckInteractions(t *testing.T) {
	features := []declaredFeature{{ID: "security.authz.deny-overrides-allow", Aliases: []string{"security.authz.deny-wins"}}}
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
		{"coveredBy alias resolves to the live ID", with(func(i *Interaction) { i.CoveredBy = []string{"security.authz.deny-wins"} }),
			"", []string{"security.authz.deny-overrides-allow"}},
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
