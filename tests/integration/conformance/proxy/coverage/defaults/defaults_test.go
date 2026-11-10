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

package defaults

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"istio.io/istio/pilot/test/xds"
)

// The flags mirror the coverage package's -update/-raise-todo. They are defined here because this
// is its own test binary.
var (
	update = flag.Bool("update", false,
		"Rewrite "+inventoryFile+": drop stale decisions and add undecided inventory paths as 'todo'. "+
			"maxTodo is never raised, so new 'todo' paths still fail the check.")
	raiseTodo = flag.Bool("raise-todo", false,
		"With -update, allow maxTodo to increase. Only for a deliberate widening of the inventory (e.g. a new "+
			"proxy type); never to silence a new default. Reviewers see it as a maxTodo increase in the diff.")
	dump = flag.Bool("dump", false, "Log every inventory path with its decision.")
)

const (
	inventoryFile = "defaults-inventory.yaml"
	todo          = "todo"
	// anyProxy as the first segment of a decision key matches every proxy type.
	anyProxy = "*"
	// prefixSuffix ends a decision key that applies to a whole subtree.
	prefixSuffix = "/*"
)

var catalogFile = filepath.Join("..", "..", "catalog.yaml")

// Inventory is the checked-in defaults-inventory.yaml.
type Inventory struct {
	// MaxTodo is a ratchet on the number of inventory paths decided "todo": it may only go down,
	// so a new default needs a feature or a reason. It counts paths, not entries, so a todo prefix
	// cannot absorb new defaults.
	MaxTodo int `json:"maxTodo"`
	// Decisions maps an inventory path, or an exempted subtree "<path>/*", to its decision. The
	// first segment may be "*" for every proxy type. The longest matching key wins.
	Decisions map[string]string `json:"decisions"`
}

// decision kinds.
const (
	kindFeature = "feature"
	kindExempt  = "exempt"
	kindTodo    = "todo"
)

// parseDecision returns the kind and argument (feature ID or reason) of a decision.
func parseDecision(d string) (kind, arg string, err error) {
	switch {
	case d == todo:
		return kindTodo, "", nil
	case strings.HasPrefix(d, "feature "):
		id := strings.TrimSpace(strings.TrimPrefix(d, "feature "))
		if id == "" || strings.ContainsAny(id, " \t") {
			return "", "", fmt.Errorf("decision %q: want 'feature <id>'", d)
		}
		return kindFeature, id, nil
	case strings.HasPrefix(d, "exempt:"):
		r := strings.TrimSpace(strings.TrimPrefix(d, "exempt:"))
		if r == "" {
			return "", "", fmt.Errorf("decision %q: exempt needs a reason", d)
		}
		return kindExempt, r, nil
	}
	return "", "", fmt.Errorf("decision %q: want 'feature <id>', 'exempt: <reason>' or 'todo'", d)
}

// matches reports whether decision key k applies to inventory path p.
func matches(k, p string) bool {
	if strings.HasPrefix(k, anyProxy+"/") {
		i := strings.Index(p, "/")
		if i < 0 {
			return false
		}
		k, p = k[2:], p[i+1:]
	}
	if pre, ok := strings.CutSuffix(k, prefixSuffix); ok {
		return p == pre || strings.HasPrefix(p, pre+"/") || strings.HasPrefix(p, pre+"[")
	}
	return k == p
}

// specificity orders matching keys: an exact key beats a prefix, a longer key beats a shorter
// one, and a proxy-specific key beats "*/".
func specificity(k string) int {
	s := len(k) * 4
	if !strings.HasSuffix(k, prefixSuffix) {
		s += 2
	}
	if !strings.HasPrefix(k, anyProxy+"/") {
		s++
	}
	return s
}

// decide returns the winning decision key for p, or "".
func (inv Inventory) decide(p string) string {
	best, bestS := "", -1
	for k := range inv.Decisions {
		if matches(k, p) {
			if s := specificity(k); s > bestS || (s == bestS && k < best) {
				best, bestS = k, s
			}
		}
	}
	return best
}

// TestDefaultsInventory checks that every default istiod configures for a sidecar, a router, a
// waypoint and ztunnel has a decision in defaults-inventory.yaml: a PCS feature that observes it,
// an exemption with a reason, or "todo". It fails when:
//   - istiod sets something new (a new inventory path) and nobody decided about it;
//   - a decision matches no inventory path (stale), or is malformed;
//   - a feature decision names an unknown feature, or one that does not test that proxy type;
//   - the number of "todo" paths exceeds maxTodo.
func TestDefaultsInventory(t *testing.T) {
	cat := readCatalog(t)
	paths := map[string][]string{} // proxy -> inventory
	// One fake istiod per profile, shared by that profile's proxy types. The server and the
	// profile's feature flags live for the profile's subtest, so it wraps the per-proxy subtests.
	for _, prof := range []profile{profileDefault, profileAmbient} {
		t.Run(prof.String(), func(t *testing.T) {
			d := newServer(t, prof)
			for _, pc := range proxyCases {
				if pc.profile != prof {
					continue
				}
				t.Run(pc.name, func(t *testing.T) {
					paths[pc.name] = proxyInventory(t, d, pc)
				})
			}
		})
	}
	if t.Failed() {
		return
	}
	var all []string
	roles := map[string][]string{}
	for _, pc := range proxyCases {
		all = append(all, paths[pc.name]...)
		roles[pc.name] = pc.roles
	}
	sort.Strings(all)

	inv := readInventory(t)
	var errs []string

	// Validate the decisions themselves.
	for k, d := range inv.Decisions {
		kind, id, err := parseDecision(d)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", k, err))
			continue
		}
		if strings.HasSuffix(k, prefixSuffix) && kind != kindExempt {
			// A feature or todo prefix would silently absorb new defaults under it.
			errs = append(errs, fmt.Sprintf("%s: only 'exempt' decisions may cover a subtree; decide %q per path", k, kind))
		}
		if kind == kindFeature {
			f, ok := cat[id]
			if !ok {
				errs = append(errs, fmt.Sprintf("%s: unknown feature %q (not an ID or alias in %s)", k, id, catalogFile))
				continue
			}
			proxy := k[:strings.Index(k+"/", "/")]
			for _, pc := range proxyCases {
				if (proxy == anyProxy || proxy == pc.name) && !overlaps(f.Roles, pc.roles) && anyMatch(k, paths[pc.name]) {
					errs = append(errs, fmt.Sprintf("%s: feature %q has roles %v, none of which test a %s (%v); "+
						"use a %s-specific key", k, id, f.Roles, pc.name, pc.roles, pc.name))
				}
			}
		}
	}

	used := map[string]bool{}
	var undecided []string
	todoPaths := map[string]bool{}
	counts := map[string]int{}
	for _, p := range all {
		k := inv.decide(p)
		if k == "" {
			undecided = append(undecided, p)
			continue
		}
		used[k] = true
		kind, _, _ := parseDecision(inv.Decisions[k])
		counts[kind]++
		if kind == kindTodo {
			todoPaths[p] = true
		}
		if *dump {
			t.Logf("%s => %s", p, inv.Decisions[k])
		}
	}
	var stale []string
	for k := range inv.Decisions {
		if !used[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(stale)

	if *update {
		for _, e := range errs {
			t.Error(e) // problems -update cannot fix
		}
		for _, k := range stale {
			delete(inv.Decisions, k)
		}
		for _, k := range newTodoKeys(undecided, all) {
			inv.Decisions[k] = todo
		}
		n := 0
		for _, p := range all {
			if k := inv.decide(p); inv.Decisions[k] == todo {
				n++
			}
		}
		if inv.MaxTodo == 0 || n < inv.MaxTodo || *raiseTodo {
			inv.MaxTodo = n
		}
		writeInventory(t, inv)
		t.Logf("updated %s (%d todo paths, maxTodo %d)", inventoryFile, n, inv.MaxTodo)
		return
	}

	for _, p := range undecided {
		errs = append(errs, fmt.Sprintf("undecided default %q: istiod configures it without user config; map it to a "+
			"feature, exempt it with a reason, or mark it todo in coverage/defaults/%s (or run with -update)", p, inventoryFile))
	}
	for _, k := range stale {
		errs = append(errs, fmt.Sprintf("stale decision %q: it matches no inventory path (or is shadowed by a more "+
			"specific key); remove it (or run with -update)", k))
	}
	if len(todoPaths) > inv.MaxTodo {
		errs = append(errs, fmt.Sprintf("%d 'todo' paths exceed maxTodo=%d: a new default needs a conformance feature or a real reason",
			len(todoPaths), inv.MaxTodo))
	}
	for _, e := range errs {
		t.Error(e)
	}
	perProxy := []string{}
	for _, pc := range proxyCases {
		perProxy = append(perProxy, fmt.Sprintf("%s %d", pc.name, len(paths[pc.name])))
	}
	t.Logf("defaults inventory: %d paths (%s): %d feature, %d exempt, %d todo (maxTodo %d)",
		len(all), strings.Join(perProxy, ", "), counts[kindFeature], counts[kindExempt], counts[kindTodo], inv.MaxTodo)
}

// proxyInventory generates pc's xDS from d and returns its inventory.
func proxyInventory(t *testing.T, d *xds.FakeDiscoveryServer, pc proxyCase) []string {
	gen := pc.generate(t, d)
	kinds := make([]string, 0, len(gen))
	for k := range gen {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var all []string
	for _, k := range kinds {
		inv := inventory(pc.name+"/"+k, gen[k])
		t.Logf("%d %s resources, %d paths", len(gen[k]), k, len(inv))
		all = append(all, inv...)
	}
	if len(all) == 0 {
		t.Fatalf("no xDS generated for %s", pc.name)
	}
	return all
}

// newTodoKeys returns the decision keys -update adds for undecided paths: "*/<rest>" when the
// path is undecided for every proxy type that has it, so one decision serves all of them, else the
// proxy-specific path.
func newTodoKeys(undecided, all []string) []string {
	und := map[string]bool{}
	for _, p := range undecided {
		und[p] = true
	}
	allUndecided := map[string]bool{}
	for _, p := range all {
		rest := p[strings.Index(p, "/")+1:]
		if v, ok := allUndecided[rest]; !ok || v {
			allUndecided[rest] = und[p]
		}
	}
	keys := map[string]bool{}
	for _, p := range undecided {
		rest := p[strings.Index(p, "/")+1:]
		if allUndecided[rest] {
			keys[anyProxy+"/"+rest] = true
		} else {
			keys[p] = true
		}
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func anyMatch(k string, paths []string) bool {
	for _, p := range paths {
		if matches(k, p) {
			return true
		}
	}
	return false
}

func overlaps(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// catalogFeature is the part of a catalog.yaml entry this check needs.
type catalogFeature struct {
	ID      string   `json:"id"`
	Roles   []string `json:"roles"`
	Aliases []string `json:"aliases"`
}

// readCatalog returns the catalog's features by ID and by alias.
func readCatalog(t *testing.T) map[string]catalogFeature {
	t.Helper()
	b, err := os.ReadFile(catalogFile)
	if err != nil {
		t.Fatal(err)
	}
	var fs []catalogFeature
	if err := yaml.Unmarshal(b, &fs); err != nil {
		t.Fatal(err)
	}
	out := map[string]catalogFeature{}
	for _, f := range fs {
		out[f.ID] = f
		for _, a := range f.Aliases {
			out[a] = f
		}
	}
	return out
}

func readInventory(t *testing.T) Inventory {
	t.Helper()
	inv := Inventory{Decisions: map[string]string{}}
	b, err := os.ReadFile(inventoryFile)
	if os.IsNotExist(err) && *update {
		return inv
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(b, &inv); err != nil {
		t.Fatal(err)
	}
	if inv.Decisions == nil {
		inv.Decisions = map[string]string{}
	}
	return inv
}

const inventoryHeader = `# Defaults istiod configures with no user config, and the proxy conformance suite's decision on each.
# Checked by TestDefaultsInventory (go test ./tests/integration/conformance/proxy/coverage/defaults/).
#
# Keys are normalized xDS paths, <proxy>/<resource>/<field>/..., generated from a minimal mesh: list
# indexes, values and per-service names dropped, extensions keyed by name ([envoy.filters...]) and
# other Any fields by type ([@Type]). "*/" matches every proxy type; "<path>/*" a whole subtree
# (exempt only). The longest key wins.
#
# Decisions:
# - "feature <id>": a PCS feature (catalog.yaml ID or alias) observes the behavior for that proxy type.
# - "exempt: <reason>": not observable black-box, or naming/bookkeeping.
# - "todo": a behavior a new proxy must replicate that PCS does not test yet. maxTodo is a ratchet on
#   todo paths: it may only go down.
# Run with -update to drop stale decisions and add new paths as todo; -raise-todo to allow maxTodo to grow.
`

func writeInventory(t *testing.T, inv Inventory) {
	t.Helper()
	b, err := yaml.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inventoryFile, append([]byte(inventoryHeader), b...), 0o644); err != nil {
		t.Fatal(err)
	}
}
