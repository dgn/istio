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

// Command pcs is the proxy conformance suite's maintainer tool. The checks in the coverage package
// say *that* something is missing; pcs turns the suite's checked-in state (the generated
// surface.yaml and coverage.yaml, the features, the declared manifests) and run artifacts into the work itself:
//
//	worklist   the uncovered surface, with each entry's documentation, for a person or an LLM to test
//	scaffold   a Feature skeleton for surface entries, to fill in
//	changed    the features a branch changed, as a -run expression for the integration tests
//	report     one row per surface entry: the features covering it and each proxy's result
//	backlog    release notes in proxy areas without a coverage decision, for triage
//
// None of them needs a cluster. Run: go run ./tests/conformance/proxy/cmd/pcs <command> -h
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"istio.io/istio/pkg/test/env"
	"istio.io/istio/tests/conformance/proxy/coverage"
)

type command struct {
	name    string
	summary string
	run     func(args []string) error
}

var commands = []command{
	{"worklist", "list uncovered surface entries with their documentation", worklist},
	{"scaffold", "print a Feature skeleton covering the given surface entries", scaffold},
	{"changed", "print the -run expression for the features changed since a git ref", changed},
	{"report", "join coverage.yaml, the declared manifests and run reports per surface entry", report},
	{"backlog", "list proxy-area release notes without a coverage decision", backlog},
}

func main() {
	if len(os.Args) > 1 {
		for _, c := range commands {
			if c.name == os.Args[1] {
				if err := c.run(os.Args[2:]); err != nil {
					fmt.Fprintf(os.Stderr, "pcs %s: %v\n", c.name, err)
					os.Exit(1)
				}
				return
			}
		}
	}
	fmt.Fprintln(os.Stderr, "usage: pcs <command> [flags]\n\ncommands:")
	for _, c := range commands {
		fmt.Fprintf(os.Stderr, "  %-9s %s\n", c.name, c.summary)
	}
	os.Exit(2)
}

// suitePath returns a path in the suite directory.
func suitePath(elem ...string) string {
	return filepath.Join(append([]string{env.IstioSrc, coverage.SuiteDir}, elem...)...)
}

// loadSurface reads the generated surface.yaml and coverage.yaml.
func loadSurface() (map[string]*coverage.Entry, error) {
	return coverage.LoadCoverage(filepath.Join(env.IstioSrc, coverage.CoverageDir))
}
