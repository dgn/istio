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
	"path"
	"path/filepath"
	"sort"
	"strings"

	"istio.io/istio/pkg/test/env"
	"istio.io/istio/tests/conformance/proxy/coverage"
)

const notesDir = "releasenotes/notes"

// untriagedNote is a proxy-area release note without a coverage decision.
type untriagedNote struct {
	name, area, text, suggestion string
}

// backlog lists the proxy-area release notes without a coverage decision, grouped by area, as a
// checklist for triage: each needs a feature, a covered-by, or a not-proxy-behavior decision, recorded
// in the note ("# pcs: ...") or, for notes older than the gate, in releasenote-backlog.yaml.
func backlog(args []string) error {
	fs := flag.NewFlagSet("backlog", flag.ContinueOnError)
	from := fs.String("from", "", "only notes added after this git ref, e.g. a release tag (default: all notes in the working tree)")
	to := fs.String("to", "HEAD", "with -from: the git ref to read notes at")
	if err := fs.Parse(args); err != nil {
		return err
	}
	bl, err := coverage.LoadBacklog(filepath.Join(env.IstioSrc, coverage.CoverageDir, coverage.BacklogFile))
	if err != nil {
		return err
	}
	notes, err := readNotes(*from, *to)
	if err != nil {
		return err
	}
	var todo []untriagedNote
	for name, b := range notes {
		if n, ok := untriaged(name, b, bl); ok {
			todo = append(todo, n)
		}
	}
	sort.Slice(todo, func(i, j int) bool {
		if todo[i].area != todo[j].area {
			return todo[i].area < todo[j].area
		}
		return todo[i].name < todo[j].name
	})
	area := ""
	for _, n := range todo {
		if n.area != area {
			area = n.area
			fmt.Printf("\n## %s\n\n", area)
		}
		fmt.Printf("- [ ] `%s`: %s\n", n.name, firstLine(n.text))
		if n.suggestion != coverage.BacklogTodo {
			fmt.Printf("  - suggested: `%s`\n", n.suggestion)
		}
	}
	fmt.Fprintf(os.Stderr, "%d of %d notes need a coverage decision\n", len(todo), len(notes))
	return nil
}

// untriaged reports whether a note is in a proxy area and has no decision, in the note or the backlog.
func untriaged(name string, note []byte, bl coverage.ReleaseNoteBacklog) (untriagedNote, bool) {
	area := coverage.NoteArea(note)
	if !coverage.ProxyAreas[area] || coverage.NoteDecisionRe.Match(note) {
		return untriagedNote{}, false
	}
	if d, ok := bl.Notes[name]; ok && d != coverage.BacklogTodo {
		return untriagedNote{}, false
	}
	return untriagedNote{name: name, area: area, text: coverage.NoteText(note), suggestion: coverage.ClassifyNote(note)}, true
}

// readNotes returns the notes by file name: all in the working tree, or those added in from..to.
func readNotes(from, to string) (map[string][]byte, error) {
	out := map[string][]byte{}
	if from == "" {
		files, err := filepath.Glob(filepath.Join(env.IstioSrc, notesDir, "*.yaml"))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			out[filepath.Base(f)] = b
		}
		return out, nil
	}
	added, err := git("log", "--diff-filter=A", "--name-only", "--format=", from+".."+to, "--", notesDir)
	if err != nil {
		return nil, err
	}
	for _, p := range strings.Fields(added) {
		b, err := git("show", to+":"+p)
		if err != nil {
			continue // removed again before to
		}
		out[path.Base(p)] = []byte(b)
	}
	return out, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	line, _, _ := strings.Cut(s, "\n")
	if len(line) > 140 {
		line = line[:140] + "..."
	}
	return line
}
