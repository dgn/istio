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
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"istio.io/istio/pkg/slices"
	"istio.io/istio/pkg/test/env"
	"istio.io/istio/tests/conformance/proxy/coverage"
)

// changed prints the go test command that runs the features a branch changed, so a PR author can run
// just those against a cluster (and their negative controls with -controls).
func changed(args []string) error {
	fs := flag.NewFlagSet("changed", flag.ContinueOnError)
	base := fs.String("base", "", "git ref to compare the working tree with (default: the merge base of HEAD and origin/master)")
	controls := fs.Bool("controls", true, "also run the negative controls of the changed features")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *base == "" {
		out, err := git("merge-base", "HEAD", "origin/master")
		if err != nil {
			return fmt.Errorf("finding the merge base with origin/master (use -base): %v", err)
		}
		*base = strings.TrimSpace(out)
	}
	diff, err := git("diff", "--unified=0", "--no-renames", *base, "--", ":(glob)"+coverage.SuiteDir+"/*_test.go",
		":(glob)"+coverage.SuiteDir+"/outcome/*.go", ":(glob,exclude)"+coverage.SuiteDir+"/outcome/*_test.go")
	if err != nil {
		return err
	}
	untracked, err := git("ls-files", "--others", "--exclude-standard", "--", ":(glob)"+coverage.SuiteDir+"/*_test.go")
	if err != nil {
		return err
	}
	suite, err := coverage.ReadSuite(suitePath())
	if err != nil {
		return err
	}
	hunks := parseDiff(diff)
	for _, f := range strings.Fields(untracked) {
		hunks = append(hunks, hunk{file: f, all: true})
	}
	sel := selectChanged(suite.Features, hunks)
	switch {
	case sel.all:
		fmt.Fprintf(os.Stderr, "the harness changed (%s): run the whole suite\n", strings.Join(sel.harness, ", "))
		fmt.Printf("go test -tags=integ ./%s/ -run '%s'\n", coverage.SuiteDir, testsPattern(suite.Roles, *controls))
	case len(sel.features) == 0:
		fmt.Fprintln(os.Stderr, "no feature changed")
	default:
		fmt.Fprintf(os.Stderr, "%d changed features: %s\n", len(sel.features), strings.Join(sel.features, ", "))
		fmt.Printf("go test -tags=integ ./%s/ -run '%s'\n", coverage.SuiteDir, runPattern(suite, sel.features, *controls))
	}
	return nil
}

// hunk is a changed line range in the new version of a file. all marks a file that is new or
// deleted as a whole.
type hunk struct {
	file       string
	start, end int
	all        bool
}

var hunkRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// parseDiff reads the changed ranges from `git diff --unified=0` output.
func parseDiff(diff string) []hunk {
	var out []hunk
	file := ""
	sc := bufio.NewScanner(strings.NewReader(diff))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "--- a/"):
			file = strings.TrimPrefix(line, "--- a/")
		case strings.HasPrefix(line, "+++ b/"):
			file = strings.TrimPrefix(line, "+++ b/")
		case line == "+++ /dev/null":
			out = append(out, hunk{file: file, all: true})
			file = ""
		case strings.HasPrefix(line, "@@ ") && file != "":
			m := hunkRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			start, _ := strconv.Atoi(m[1])
			count := 1
			if m[2] != "" {
				count, _ = strconv.Atoi(m[2])
			}
			h := hunk{file: file, start: start, end: start + count - 1}
			if count == 0 {
				// A pure deletion after line start: it touches the lines on both sides.
				h.end = start + 1
			}
			out = append(out, h)
		}
	}
	return out
}

// selection is what changed: the IDs of changed features, or all when a harness file changed.
type selection struct {
	features []string
	all      bool
	harness  []string
}

// selectChanged maps changed ranges to the Feature literals they touch. A change in a features_*
// file outside any literal (a helper of that file's features) selects all of the file's features; a
// change in any other file of the suite changes the harness, which every feature runs on.
func selectChanged(features []coverage.Feature, hunks []hunk) selection {
	var sel selection
	ids := map[string]bool{}
	for _, h := range hunks {
		base := filepath.Base(h.file)
		if !strings.HasPrefix(base, "features_") || filepath.Dir(h.file) != coverage.SuiteDir {
			sel.all = true
			sel.harness = append(sel.harness, h.file)
			continue
		}
		inFile := slices.Filter(features, func(f coverage.Feature) bool { return filepath.Base(f.File) == base })
		hit := false
		for _, f := range inFile {
			if h.all || h.start <= f.EndLine && f.Line <= h.end {
				ids[f.ID] = true
				hit = true
			}
		}
		if !hit {
			for _, f := range inFile {
				ids[f.ID] = true
			}
		}
	}
	for id := range ids {
		sel.features = append(sel.features, id)
	}
	sort.Strings(sel.features)
	sel.harness = slices.FilterDuplicates(sel.harness)
	return sel
}

// testsPattern matches the conformance tests of all roles.
func testsPattern(roles []string, controls bool) string {
	var names []string
	for _, r := range roles {
		names = append(names, strings.TrimSuffix(strings.TrimPrefix(coverage.RoleTestName(r), "Test"), "Conformance"))
	}
	suffix := ""
	if controls {
		suffix = "(Controls)?"
	}
	return fmt.Sprintf("^Test(%s)Conformance%s$", strings.Join(names, "|"), suffix)
}

// runPattern is a -run expression for the features: the role tests that run them, any proxy, and
// the feature subtests.
func runPattern(suite *coverage.Suite, ids []string, controls bool) string {
	var roles []string
	for _, f := range suite.Features {
		if slices.Contains(ids, f.ID) {
			roles = append(roles, f.Roles...)
		}
	}
	roles = slices.Sort(slices.FilterDuplicates(roles))
	quoted := make([]string, 0, len(ids))
	for _, id := range ids {
		quoted = append(quoted, regexp.QuoteMeta(id))
	}
	return fmt.Sprintf("%s/.*/^(%s)$", testsPattern(roles, controls), strings.Join(quoted, "|"))
}

func git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = env.IstioSrc
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, ee.Stderr)
	}
	return string(out), err
}
