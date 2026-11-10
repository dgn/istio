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
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// DataDir holds the checked-in decisions and generated views of the coverage check, relative to this
// package.
const DataDir = "data"

// CoverageDir is DataDir relative to the repository root.
const CoverageDir = SuiteDir + "/coverage/" + DataDir

// Files in DataDir. The first three are decisions people make and review; the others are generated
// by TestCoverage -update and checked for staleness.
const (
	ExemptionsFile = "exemptions.yaml"
	ScopeFile      = "scope.yaml"
	RatchetsFile   = "ratchets.yaml"
	SurfaceFile    = "surface.yaml"
	CoverageFile   = "coverage.yaml"
	SummaryFile    = "summary.yaml"
)

// Exemptions is exemptions.yaml: the surface the suite deliberately does not cover, each with a real
// reason. Uncovered surface without an exemption is the backlog ("todo"); it is derived, not listed.
type Exemptions struct {
	// Exempt maps a surface entry, a prefix ending in "*" (a whole subtree or annotation family), or
	// "key:<string>" (see TestExtensionKeysTracked) to the reason it is not covered by a feature.
	Exempt map[string]string `json:"exempt"`
}

// Scope is scope.yaml: which roles must each cover which surface. Without it, coverage would be a
// union across roles, so a field covered by an ingress feature would count as covered for a sidecar
// that was never tested with it.
type Scope struct {
	// Default are the roles in scope for entries no roleScope key matches.
	Default []string `json:"default"`
	// RoleScope maps a surface prefix ("ServiceEntry", "AuthorizationPolicy.action=DENY") to the roles
	// that must each cover the matching entries with a feature of their own. The longest matching key
	// wins, so a narrower key can widen or narrow the role list of a subtree. A key ending in "*"
	// matches by plain string prefix. interactions.yaml adds implicit keys for interaction:<id>.
	RoleScope map[string][]string `json:"roleScope,omitempty"`
}

// Ratchets is ratchets.yaml: limits that may only go down. TestCoverage -update lowers them; only
// -raise-todo raises them, which reviewers see as an increase in the diff.
type Ratchets struct {
	// MaxTodo bounds the backlog: surface entries neither covered nor exempt.
	MaxTodo int `json:"maxTodo" yaml:"maxTodo"`
	// MaxRoleGaps bounds, per role, the in-scope entries the role does not cover.
	MaxRoleGaps map[string]int `json:"maxRoleGaps" yaml:"maxRoleGaps"`
}

// Config is the checked-in input of the coverage check besides the features and the surface.
type Config struct {
	Exemptions Exemptions
	Scope      Scope
	Ratchets   Ratchets
}

// LoadConfig reads exemptions.yaml, scope.yaml and ratchets.yaml from dir. Missing files are empty.
func LoadConfig(dir string) (Config, error) {
	var c Config
	for file, into := range map[string]any{ExemptionsFile: &c.Exemptions, ScopeFile: &c.Scope, RatchetsFile: &c.Ratchets} {
		b, err := os.ReadFile(filepath.Join(dir, file))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return c, err
		}
		if err := yaml.UnmarshalStrict(b, into); err != nil {
			return c, fmt.Errorf("%s: %v", file, err)
		}
	}
	if c.Exemptions.Exempt == nil {
		c.Exemptions.Exempt = map[string]string{}
	}
	if c.Ratchets.MaxRoleGaps == nil {
		c.Ratchets.MaxRoleGaps = map[string]int{}
	}
	return c, nil
}

const exemptionsHeader = `# API surface the proxy conformance suite deliberately does not cover, with the reason.
# Checked by TestCoverage (go test ./tests/conformance/proxy/coverage/). Every reason must
# be real: uncovered surface without an exemption is the backlog, which is derived (status "todo" in
# coverage.yaml) and bounded by maxTodo in ratchets.yaml.
#
# - Entries ending in "*" exempt every entry with that prefix: "<Kind>.<path>.*" a whole subtree
#   (e.g. sidecar-only MeshConfig sections), "annotation:<prefix>*" an annotation family.
# - "key:<string>" entries exempt Istio-specific keys found in the Gateway API translators.
# - Run TestCoverage with -update to drop stale or redundant (covered) entries.
`

const scopeHeader = `# Which roles must each cover which API surface with a feature of their own (TestCoverage).
#
# - default: the roles for entries that no roleScope key matches.
# - roleScope keys match an entry and its fields/enum values; the longest matching key wins; a key
#   ending in "*" matches by plain prefix. interactions.yaml adds implicit keys for interaction:<id>
#   from each interaction's roles; an explicit key here wins.
# - An exempt entry is out of scope for every role. The resulting per-role gaps are listed in
#   coverage.yaml and bounded by maxRoleGaps in ratchets.yaml.
`

const ratchetsHeader = `# Ratchets of the coverage check (TestCoverage). They may only go down: -update lowers them, and only
# -update -raise-todo raises them, for deliberate surface expansions (e.g. tracking a new API).
#
# - maxTodo: surface entries neither covered by a feature nor exempt (status "todo" in coverage.yaml).
# - maxRoleGaps: per role, in-scope entries (scope.yaml) that no feature of the role covers.
`

// Write writes the three files to dir.
func (c Config) Write(dir string) error {
	for _, f := range []struct {
		name, header string
		v            any
	}{
		{ExemptionsFile, exemptionsHeader, c.Exemptions},
		{ScopeFile, scopeHeader, c.Scope},
		{RatchetsFile, ratchetsHeader, c.Ratchets},
	} {
		b, err := yaml.Marshal(f.v)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, f.name), append([]byte(f.header), b...), 0o644); err != nil {
			return err
		}
	}
	return nil
}
