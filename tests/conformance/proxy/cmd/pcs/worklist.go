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
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"os"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"istio.io/istio/pkg/slices"
	"istio.io/istio/tests/conformance/proxy/coverage"
)

// workItem is one surface entry to cover.
type workItem struct {
	Key    string   `json:"key"`
	Source string   `json:"source"`
	Status string   `json:"status"`
	Gaps   []string `json:"gaps,omitempty"`
	// CoveredBy are features of other roles that already cover the entry: a starting point.
	CoveredBy []string `json:"coveredBy,omitempty"`
	Doc       string   `json:"doc,omitempty"`
}

// worklist prints the surface entries that still need a feature: the todo backlog, or with -role the
// entries in that role's scope that no feature of the role covers.
func worklist(args []string) error {
	fs := flag.NewFlagSet("worklist", flag.ContinueOnError)
	role := fs.String("role", "", "list the gaps of this role (entries in its scope it does not cover) instead of the todo backlog")
	source := fs.String("source", "", "only entries from this source (proto, feature-flags, annotation-registry, label-registry, extension-registry, manual)")
	prefix := fs.String("prefix", "", "only entries with this key prefix, e.g. VirtualService.http.retries")
	n := fs.Int("n", 0, "at most this many entries (0: all)")
	output := fs.String("o", "text", "output format: text or yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}
	surface, err := loadSurface()
	if err != nil {
		return err
	}
	d, err := newDocs()
	if err != nil {
		return err
	}
	items := selectWork(surface, *role, *source, *prefix)
	if *n > 0 && len(items) > *n {
		items = items[:*n]
	}
	for i := range items {
		items[i].Doc = d.of(items[i].Key)
	}
	switch *output {
	case "yaml":
		b, err := yaml.Marshal(items)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(b)
		return err
	case "text":
		for _, it := range items {
			fmt.Printf("%s  [%s, %s", it.Key, it.Source, it.Status)
			if len(it.Gaps) > 0 {
				fmt.Printf(", gaps: %s", strings.Join(it.Gaps, ","))
			}
			if len(it.CoveredBy) > 0 {
				fmt.Printf(", covered elsewhere by: %s", strings.Join(it.CoveredBy, ","))
			}
			fmt.Println("]")
			if it.Doc != "" {
				fmt.Println(indent(it.Doc, "    "))
			}
			fmt.Println()
		}
		fmt.Fprintf(os.Stderr, "%d entries\n", len(items))
		return nil
	default:
		return fmt.Errorf("unknown output format %q", *output)
	}
}

// selectWork returns the entries to work on, sorted by key.
func selectWork(surface map[string]*coverage.Entry, role, source, prefix string) []workItem {
	var out []workItem
	for key, e := range surface {
		if source != "" && e.Source != source || !strings.HasPrefix(key, prefix) {
			continue
		}
		if role != "" && !slices.Contains(e.Gaps, role) || role == "" && e.Status != coverage.EntryTodo {
			continue
		}
		it := workItem{Key: key, Source: e.Source, Status: e.Status, Gaps: e.Gaps}
		for _, fs := range e.CoveredBy {
			it.CoveredBy = append(it.CoveredBy, fs...)
		}
		it.CoveredBy = slices.Sort(slices.FilterDuplicates(it.CoveredBy))
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}

// scaffold prints a Feature literal covering the given surface entries, with their documentation in
// the Run stub, to paste into a features_<area>_test.go file and fill in.
func scaffold(args []string) error {
	fs := flag.NewFlagSet("scaffold", flag.ContinueOnError)
	role := fs.String("role", "ingress", "role of the feature")
	id := fs.String("id", "", "feature ID (default: derived from the first entry, under TODO.)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	keys := fs.Args()
	if len(keys) == 0 {
		return fmt.Errorf("usage: pcs scaffold [-role r] [-id id] <surface entry> [<surface entry> ...]")
	}
	surface, err := loadSurface()
	if err != nil {
		return err
	}
	for _, k := range keys {
		if surface[k] == nil {
			return fmt.Errorf("%q is not a surface entry (see %s/%s)", k, coverage.CoverageDir, coverage.SurfaceFile)
		}
	}
	d, err := newDocs()
	if err != nil {
		return err
	}
	src, err := scaffoldSource(*role, *id, keys, d.of)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(src)
	return err
}

// scaffoldSource returns the formatted Feature literal.
func scaffoldSource(role, id string, keys []string, doc func(string) string) ([]byte, error) {
	if id == "" {
		id = "TODO." + scaffoldID(keys[0])
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "package p\n\nvar _ = Feature{\nID: %q,\n", id)
	b.WriteString("Description: \"TODO: the behavior a user observes, in one sentence.\",\n")
	fmt.Fprintf(&b, "Roles: []Role{Role%s},\n", strings.ToUpper(role[:1])+role[1:])
	b.WriteString("Covers: []string{\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%q,\n", k)
	}
	b.WriteString("},\nRun: func(t framework.TestContext, s *Scenario) {\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "// %s:\n", k)
		if text := doc(k); text != "" {
			b.WriteString(indent(text, "//   ") + "\n")
		}
		b.WriteString("//\n")
	}
	b.WriteString(`// TODO: set up a target (s.Target), apply config that sets every entry in Covers (s.Apply; the
// harness fails a supported run whose applied config leaves one unset), and assert the behavior
// (tg.Expect). The negative control reruns the feature with its config removed: the assertions
// must fail there, or set NoControl with the reason.
t.Fatal("TODO: implement")
},
}
`)
	src, err := format.Source(b.Bytes())
	if err != nil {
		return nil, err
	}
	// Drop the wrapper: print just the literal, to paste into a Register call.
	_, lit, _ := strings.Cut(string(src), "var _ = ")
	return []byte(lit), nil
}

// scaffoldID derives a feature ID suffix from a surface key, e.g. "VirtualService.http.retries" ->
// "virtualservice.http-retries".
func scaffoldID(key string) string {
	key = strings.NewReplacer("=", "-", "_", "-", ":", ".", "/", "-").Replace(key)
	kind, rest, found := strings.Cut(key, ".")
	if !found {
		return strings.ToLower(key)
	}
	return strings.ToLower(kind) + "." + strings.ToLower(strings.ReplaceAll(rest, ".", "-"))
}
