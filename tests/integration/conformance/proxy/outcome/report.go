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

package outcome

import (
	"os"
	"path/filepath"
	"sort"

	"sigs.k8s.io/yaml"
)

// Report is the machine-readable result of one conformance run of a role. It is modeled on the
// Gateway API ConformanceReport (implementation, profiles, per-profile summary), extended with the
// outcome taxonomy and the evidence behind each result. Reports are CI artifacts; per release they
// are published under reports/<istio-version>/ and render the istio.io support matrix.
type Report struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Date       string `json:"date"`
	// IstioVersion identifies the istiod build under test (image hub/tag).
	IstioVersion string `json:"istioVersion"`
	Role         string `json:"role"`
	// Implementations holds one entry per proxy implementation run in this role.
	Implementations []Implementation `json:"implementations"`
}

// Implementation is the result for one proxy implementation in a role.
type Implementation struct {
	Name string `json:"name"`
	// Profiles summarizes outcomes per conformance profile (core, extended, impl-specific).
	Profiles []ProfileSummary `json:"profiles"`
	Results  []Result         `json:"results"`
}

// ProfileSummary counts outcomes of the features in one profile.
type ProfileSummary struct {
	Name     string          `json:"name"`
	Outcomes map[Outcome]int `json:"outcomes"`
}

// Result is the classified result of one feature, with the declared support it was compared to.
type Result struct {
	Feature  string `json:"feature"`
	Profile  string `json:"profile"`
	Observed `json:",inline"`
	Declared string       `json:"declared,omitempty"`
	Decision DecisionKind `json:"decision"`
	// Attempts is the number of runs it took to classify (1, plus a rerun and a control on failure).
	Attempts        int     `json:"attempts"`
	DurationSeconds float64 `json:"durationSeconds"`
}

// Summarize fills Profiles from Results, and sorts results by feature.
func (i *Implementation) Summarize() {
	sort.Slice(i.Results, func(a, b int) bool { return i.Results[a].Feature < i.Results[b].Feature })
	byProfile := map[string]map[Outcome]int{}
	for _, r := range i.Results {
		if byProfile[r.Profile] == nil {
			byProfile[r.Profile] = map[Outcome]int{}
		}
		byProfile[r.Profile][r.Outcome]++
	}
	i.Profiles = nil
	for name, counts := range byProfile {
		i.Profiles = append(i.Profiles, ProfileSummary{Name: name, Outcomes: counts})
	}
	sort.Slice(i.Profiles, func(a, b int) bool { return i.Profiles[a].Name < i.Profiles[b].Name })
}

// Write writes the report as YAML.
func (r Report) Write(path string) error {
	r.APIVersion, r.Kind = "pcs.istio.io/v1alpha1", "ProxyConformanceReport"
	for i := range r.Implementations {
		r.Implementations[i].Summarize()
	}
	sort.Slice(r.Implementations, func(a, b int) bool { return r.Implementations[a].Name < r.Implementations[b].Name })
	b, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
