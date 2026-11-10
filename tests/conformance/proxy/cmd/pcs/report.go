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

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"istio.io/istio/pkg/slices"
	"istio.io/istio/tests/conformance/proxy/coverage"
	"istio.io/istio/tests/conformance/proxy/outcome"
)

// statusGap marks an entry in a role's scope that no feature of the role covers.
const statusGap = "gap"

// statusRank orders statuses from worst to best, to summarize an entry covered by several features
// by its worst one. Inconclusive outcomes rank like unknown.
var statusRank = map[string]int{
	statusGap:                     0,
	coverage.StatusIgnored:        1,
	coverage.StatusFail:           2,
	coverage.StatusFailClosed:     3,
	coverage.StatusRejected:       4,
	coverage.StatusUnsupported:    5,
	coverage.StatusPartial:        6,
	coverage.StatusUnknown:        7,
	string(outcome.Indeterminate): 7,
	string(outcome.Flaky):         7,
	string(outcome.Skipped):       7,
	coverage.StatusSupported:      8,
}

// reportRow is one surface entry in one role.
type reportRow struct {
	Entry    string   `json:"entry"`
	Features []string `json:"features,omitempty"`
	// Status maps an implementation to the worst status of the features covering the entry.
	Status map[string]string `json:"status"`
}

// roleReport is the report of one role.
type roleReport struct {
	Role            string      `json:"role"`
	Implementations []string    `json:"implementations"`
	Rows            []reportRow `json:"rows"`
}

// report joins the surface with the declared manifests and, with -reports, the observed results of a
// run: per role, every entry in its scope with each implementation's status. Observed results take
// precedence over declarations.
func report(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	role := fs.String("role", "", "only this role")
	reports := fs.String("reports", "", "directory with pcs-report-<role>.yaml files of a run; their results override the declared ones")
	output := fs.String("o", "md", "output format: md or yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}
	surface, err := loadSurface()
	if err != nil {
		return err
	}
	suite, err := coverage.ReadSuite(suitePath())
	if err != nil {
		return err
	}
	var out []roleReport
	for _, r := range suite.Roles {
		impls := suite.Proxies[r]
		if *role != "" && r != *role || len(impls) == 0 {
			continue
		}
		status := map[string]map[string]string{} // impl -> feature -> status
		for _, impl := range impls {
			m, err := coverage.LoadManifest(coverage.ManifestPath(suitePath(), r, impl))
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			status[impl] = map[string]string{}
			for id, d := range m.Features {
				status[impl][id] = d.Status
			}
		}
		if *reports != "" {
			if err := applyObserved(filepath.Join(*reports, "pcs-report-"+r+".yaml"), status); err != nil {
				return err
			}
		}
		out = append(out, buildRoleReport(r, impls, surface, status))
	}
	switch *output {
	case "yaml":
		b, err := yaml.Marshal(out)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(b)
		return err
	case "md":
		for _, rr := range out {
			writeMarkdown(rr)
		}
		return nil
	default:
		return fmt.Errorf("unknown output format %q", *output)
	}
}

// applyObserved overrides declared statuses with the outcomes of a run report, if it exists.
func applyObserved(path string, status map[string]map[string]string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var rep outcome.Report
	if err := yaml.Unmarshal(b, &rep); err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	for _, impl := range rep.Implementations {
		if status[impl.Name] == nil {
			status[impl.Name] = map[string]string{}
		}
		for _, res := range impl.Results {
			status[impl.Name][res.Feature] = string(res.Outcome)
		}
	}
	return nil
}

// buildRoleReport returns the rows of the entries in the role's scope or covered by its features.
func buildRoleReport(role string, impls []string, surface map[string]*coverage.Entry, status map[string]map[string]string) roleReport {
	rr := roleReport{Role: role, Implementations: impls}
	for key, e := range surface {
		features := e.CoveredBy[role]
		if e.Status == coverage.EntryExempt || len(features) == 0 && !slices.Contains(e.Gaps, role) {
			continue
		}
		row := reportRow{Entry: key, Features: features, Status: map[string]string{}}
		for _, impl := range impls {
			row.Status[impl] = worstStatus(features, status[impl])
		}
		rr.Rows = append(rr.Rows, row)
	}
	sort.Slice(rr.Rows, func(i, j int) bool { return rr.Rows[i].Entry < rr.Rows[j].Entry })
	return rr
}

// worstStatus returns the worst status of the features, "unknown" for a feature without one, and
// "gap" without features.
func worstStatus(features []string, status map[string]string) string {
	if len(features) == 0 {
		return statusGap
	}
	worst := ""
	for _, f := range features {
		s := status[f]
		if s == "" {
			s = coverage.StatusUnknown
		}
		if worst == "" || statusRank[s] < statusRank[worst] {
			worst = s
		}
	}
	return worst
}

func writeMarkdown(rr roleReport) {
	fmt.Printf("## %s\n\n", rr.Role)
	for _, impl := range rr.Implementations {
		counts := map[string]int{}
		for _, row := range rr.Rows {
			counts[row.Status[impl]]++
		}
		statuses := make([]string, 0, len(counts))
		for s := range counts {
			statuses = append(statuses, s)
		}
		sort.Slice(statuses, func(i, j int) bool { return statusRank[statuses[i]] > statusRank[statuses[j]] })
		parts := make([]string, 0, len(statuses))
		for _, s := range statuses {
			parts = append(parts, fmt.Sprintf("%s %d", s, counts[s]))
		}
		fmt.Printf("- **%s** (%d entries): %s\n", impl, len(rr.Rows), strings.Join(parts, ", "))
	}
	fmt.Printf("\n| entry | features | %s |\n|---|---|%s\n", strings.Join(rr.Implementations, " | "), strings.Repeat("---|", len(rr.Implementations)))
	for _, row := range rr.Rows {
		cells := make([]string, 0, len(rr.Implementations))
		for _, impl := range rr.Implementations {
			cells = append(cells, row.Status[impl])
		}
		fmt.Printf("| `%s` | %s | %s |\n", row.Entry, strings.Join(row.Features, ", "), strings.Join(cells, " | "))
	}
	fmt.Println()
}
