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
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestDeclaredSupport keeps the declared support manifests (declared/<role>/<impl>.yaml, design doc
// §3.6) in step with the features, so every implementation owner makes an explicit decision about
// every feature of their role. The integration suite holds the declarations to observed behavior;
// this test holds them to the feature list, in `make test`. It fails on:
//   - a role without manifests, a role whose manifests don't mark exactly one reference, a proxy in
//     the role's proxy table without a manifest, or a manifest for an implementation not in it;
//   - a feature of the role missing from a manifest (-update adds it as "unknown", but only in
//     non-reference manifests: the reference defines the feature, so someone must run it there);
//   - entries for features that don't exist or don't have that role (-update removes them, and
//     renames an entry keyed by a feature's alias to the feature's current ID);
//   - an invalid status, or a missing reason/issue;
//   - more "unknown" entries than maxUnknown (-update lowers it; only -raise-todo raises it).
func TestDeclaredSupport(t *testing.T) {
	features, roles := readFeatures(t)
	proxies := readProxies(t)

	aliasOf := map[string]string{}
	for _, f := range features {
		for _, a := range f.Aliases {
			aliasOf[a] = f.ID
		}
	}

	var errs []string
	knownRoles := map[string]bool{}
	for _, role := range roles {
		knownRoles[role] = true
		roleFeatures := map[string]bool{}
		for _, f := range features {
			for _, r := range f.Roles {
				if r == role {
					roleFeatures[f.ID] = true
				}
			}
		}

		files, err := filepath.Glob(filepath.Join(suiteDir, DeclaredDir, role, "*.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			errs = append(errs, fmt.Sprintf("role %q has no declared support manifests: add %s for each implementation, one with reference: true",
				role, ManifestPath(suiteDir, role, "<impl>")))
			continue
		}
		impls := map[string]bool{}
		refs := 0
		for _, path := range files {
			impl := strings.TrimSuffix(filepath.Base(path), ".yaml")
			impls[impl] = true
			if !contains(proxies[role], impl) {
				errs = append(errs, fmt.Sprintf("%s: %q is not an implementation of role %q in proxies_test.go: remove the manifest or add the proxy",
					path, impl, role))
			}
			m, err := LoadManifest(path)
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			if m.Reference {
				refs++
			}
			errs = append(errs, checkManifest(t, path, role, m, roleFeatures, aliasOf)...)
		}
		for _, impl := range proxies[role] {
			if !impls[impl] {
				errs = append(errs, fmt.Sprintf("proxy %q of role %q has no declared support manifest: add %s",
					impl, role, ManifestPath(suiteDir, role, impl)))
			}
		}
		if refs != 1 {
			errs = append(errs, fmt.Sprintf("role %q has %d reference manifests: exactly one implementation per role must set reference: true", role, refs))
		}
	}

	dirs, err := filepath.Glob(filepath.Join(suiteDir, DeclaredDir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if !knownRoles[filepath.Base(d)] {
			errs = append(errs, fmt.Sprintf("%s: %q is not a role: remove it", d, filepath.Base(d)))
		}
	}

	sort.Strings(errs)
	for _, e := range errs {
		t.Error(e)
	}
}

// checkManifest validates one manifest against the role's features and, with -update, fixes what can
// be fixed mechanically and rewrites it.
func checkManifest(t *testing.T, path, role string, m Manifest, roleFeatures map[string]bool, aliasOf map[string]string) []string {
	t.Helper()
	var errs []string
	ids := make([]string, 0, len(m.Features))
	for id := range m.Features {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		if roleFeatures[id] {
			continue
		}
		if to, ok := aliasOf[id]; ok && roleFeatures[to] {
			if !*update {
				errs = append(errs, fmt.Sprintf("%s: %q is a former ID of %q: rename the entry (or run with -update)", path, id, to))
				continue
			}
			if _, exists := m.Features[to]; !exists {
				m.Features[to] = m.Features[id]
			}
			delete(m.Features, id)
			continue
		}
		if !*update {
			errs = append(errs, fmt.Sprintf("%s: stale entry %q: no feature of role %q has this ID; remove it (or run with -update)", path, id, role))
			continue
		}
		delete(m.Features, id)
	}

	var missing []string
	for id := range roleFeatures {
		if _, ok := m.Features[id]; !ok {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	for _, id := range missing {
		switch {
		case m.Reference:
			errs = append(errs, fmt.Sprintf("%s: feature %q is not declared in the reference implementation: run it against the reference and declare the result "+
				"(go test -tags=integ ./tests/integration/conformance/proxy/ -run %s -istio.test.pcs.update-declared)", path, id, roleTestName(role)))
		case *update:
			m.Features[id] = Declared{Status: StatusUnknown}
		default:
			errs = append(errs, fmt.Sprintf("%s: feature %q has no entry: declare its status (or run with -update to add it as %q)", path, id, StatusUnknown))
		}
	}

	unknown := 0
	ids = ids[:0]
	for id, d := range m.Features {
		ids = append(ids, id)
		if d.Status == StatusUnknown {
			unknown++
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := m.Features[id].Validate(id); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s: %v", path, id, err))
		}
	}

	if *update {
		if unknown < m.MaxUnknown || *raiseTodo {
			m.MaxUnknown = unknown
		}
		if err := m.Write(path); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s (%d unknown, maxUnknown %d)", path, unknown, m.MaxUnknown)
	}
	if unknown > m.MaxUnknown {
		errs = append(errs, fmt.Sprintf("%s: %d 'unknown' entries exceed maxUnknown=%d: declare the new features' status after running them", path, unknown, m.MaxUnknown))
	}
	return errs
}

// readProxies parses the suite's proxy tables ([]Proxy literals in proxies_test.go) and returns the
// implementation names per role, so every proxy under test has a manifest.
func readProxies(t *testing.T) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob(filepath.Join(suiteDir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	rt := readRoleTable(t, files, fset)
	out := map[string][]string{}
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
			at, ok := cl.Type.(*ast.ArrayType)
			if !ok {
				return true
			}
			if id, ok := at.Elt.(*ast.Ident); !ok || id.Name != "Proxy" {
				return true
			}
			for _, el := range cl.Elts {
				p, ok := el.(*ast.CompositeLit)
				if !ok {
					continue
				}
				var name, role string
				for _, e := range p.Elts {
					kv, ok := e.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					switch kv.Key.(*ast.Ident).Name {
					case "Name":
						name = stringLit(t, fset, kv.Value)
					case "Role":
						if id, ok := kv.Value.(*ast.Ident); ok {
							role = rt.consts[id.Name]
						}
					}
				}
				if name == "" || role == "" {
					t.Fatalf("%s: Proxy literal needs a string Name and a Role constant (it is read from source)", fset.Position(p.Pos()))
				}
				if !contains(out[role], name) {
					out[role] = append(out[role], name)
				}
			}
			return false
		})
	}
	if len(out) == 0 {
		t.Fatal("no []Proxy tables found; is suiteDir correct?")
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestManifestRoundTrip checks that Write and LoadManifest are inverse and deterministic, since the
// integration suite rewrites manifests and an unstable encoding would churn every review.
func TestManifestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ingress", "impl.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	m := Manifest{MaxUnknown: 1, Features: map[string]Declared{
		"b": {Status: StatusUnknown},
		"a": {Status: StatusPartial, Reason: "exact only", Cases: []string{"exact"}},
	}}
	if err := m.Write(path); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	got, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Write(path); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Fatalf("rewrite changed the manifest:\n%s\n---\n%s", first, second)
	}
	if !strings.Contains(string(first), "-run TestIngressConformance -istio.test.pcs.update-declared") {
		t.Fatalf("header lacks the refresh command:\n%s", first)
	}
}
