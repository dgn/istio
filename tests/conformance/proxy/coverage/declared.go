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
	"strings"

	"sigs.k8s.io/yaml"
)

// DeclaredDir is the directory, relative to the suite, that holds the declared support manifests:
// <DeclaredDir>/<role>/<implementation>.yaml.
const DeclaredDir = "declared"

// Declared support statuses. They mirror the outcomes of architecture/tests/proxy-conformance.md
// §3.6, plus "unknown" for features nobody has measured yet.
const (
	StatusSupported   = "supported"
	StatusPartial     = "partial"
	StatusRejected    = "rejected"
	StatusFailClosed  = "fail-closed"
	StatusIgnored     = "ignored"
	StatusFail        = "fail"
	StatusUnsupported = "unsupported"
	StatusUnknown     = "unknown"
)

// statuses is the set of valid Declared.Status values.
var statuses = map[string]bool{
	StatusSupported: true, StatusPartial: true, StatusRejected: true, StatusFailClosed: true,
	StatusIgnored: true, StatusFail: true, StatusUnsupported: true, StatusUnknown: true,
}

// Manifest is an implementation's declared support for the features of one role. It is checked in,
// so every change in what an implementation supports shows up in review, and every new feature forces
// each implementation owner to make an explicit decision (TestDeclaredSupport). The integration
// suite reads it to compare observed with declared support and rewrites it with
// -istio.test.pcs.update-declared; this package (no build tag) validates it in `make test`.
type Manifest struct {
	// Reference marks the reference implementation of the role. Exactly one manifest per role sets it.
	// A feature of the role must be declared in the reference manifest by a person who ran it: it is
	// never added as "unknown" automatically, since the reference defines what the feature means.
	Reference bool `json:"reference,omitempty"`
	// MaxUnknown is a ratchet: the number of "unknown" entries may only go down.
	MaxUnknown int `json:"maxUnknown"`
	// Features maps a feature ID to its declared status.
	Features map[string]Declared `json:"features"`
}

// Declared is the declared support for one feature.
type Declared struct {
	Status string `json:"status"`
	// Reason explains any status other than supported and unknown, so a reader of the matrix knows
	// what users will experience.
	Reason string `json:"reason,omitempty"`
	// Issue optionally tracks the fix of a gap.
	Issue string `json:"issue,omitempty"`
	// Cases lists the passing subcases of a partial feature.
	Cases []string `json:"cases,omitempty"`
	// Kinds are the API kinds (QualifiedKind, or a pseudo kind such as KindMeshConfig) the feature
	// applied when last run against the reference implementation, scaffolding included. Only the
	// reference manifest has them; -istio.test.pcs.update-declared writes them. None means the
	// feature applies no config.
	Kinds []string `json:"kinds,omitempty"`
}

// Validate reports what is wrong with the declaration of a feature, or nil.
func (d Declared) Validate() error {
	if !statuses[d.Status] {
		return fmt.Errorf("invalid status %q (want one of supported, partial, rejected, fail-closed, ignored, fail, unsupported, unknown)", d.Status)
	}
	if d.Status != StatusSupported && d.Status != StatusUnknown && strings.TrimSpace(d.Reason) == "" {
		return fmt.Errorf("status %q needs a reason", d.Status)
	}
	if len(d.Cases) > 0 && d.Status != StatusPartial {
		return fmt.Errorf("cases are only meaningful for status %q", StatusPartial)
	}
	return nil
}

// ManifestPath returns the manifest path for an implementation in a role, relative to suiteDir.
func ManifestPath(suiteDir, role, impl string) string {
	return filepath.Join(suiteDir, DeclaredDir, role, impl+".yaml")
}

// LoadManifest reads a manifest. Features is never nil.
func LoadManifest(path string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err := yaml.UnmarshalStrict(b, &m); err != nil {
		return m, fmt.Errorf("%s: %v", path, err)
	}
	if m.Features == nil {
		m.Features = map[string]Declared{}
	}
	return m, nil
}

// Write writes the manifest with a generated header. The output is deterministic (map keys are
// sorted), so rewriting an unchanged manifest produces no diff; comments other than the header are
// not preserved.
func (m Manifest) Write(path string) error {
	if m.Features == nil {
		m.Features = map[string]Declared{}
	}
	b, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(manifestHeader(path)), b...), 0o644)
}

// manifestHeader explains the file to someone who opens it, derived from its path
// (declared/<role>/<impl>.yaml).
func manifestHeader(path string) string {
	impl := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	role := filepath.Base(filepath.Dir(path))
	return fmt.Sprintf(`# Declared support of %[1]s in the %[2]s role for the proxy conformance suite features.
# Checked by TestDeclaredSupport (go test ./tests/conformance/proxy/coverage/).
#
# - status: supported|partial|rejected|fail-closed|ignored|fail|unsupported|unknown. Every status
#   except supported and unknown needs a reason; issue optionally tracks the fix.
#   cases lists the passing subcases of a partial feature.
# - maxUnknown is a ratchet: the number of unknown entries may only go down.
# - reference: true marks the role's reference implementation; its entries are never added as unknown.
#   Its kinds (generated) list the API kinds the feature applied in the last reference run.
#
# Refresh from a run against a cluster (keeps reason/issue when the status is unchanged):
#   go test -tags=integ ./tests/conformance/proxy/ -run %[3]s -istio.test.pcs.update-declared
# Add new features / drop removed ones (non-reference files only add unknown entries):
#   go test ./tests/conformance/proxy/coverage/ -run TestDeclaredSupport -update
`, impl, role, RoleTestName(role))
}

// RoleTestName is the integration test that runs a role, e.g. TestIngressConformance.
func RoleTestName(role string) string {
	if role == "" {
		return "Test<Role>Conformance"
	}
	return "Test" + strings.ToUpper(role[:1]) + role[1:] + "Conformance"
}
