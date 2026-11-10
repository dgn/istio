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
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	yamlv3 "gopkg.in/yaml.v3"
	"sigs.k8s.io/yaml"

	"istio.io/istio/pkg/test/env"
)

const (
	notesDir = "releasenotes/notes"
)

var (
	areaRe = regexp.MustCompile(`(?m)^area:\s*(\S+)\s*$`)
	// pcsRe matches the decision comment in a release note, e.g. "# pcs: feature egress.tls-origination".
	pcsRe = regexp.MustCompile(`(?m)^\s*#\s*pcs:\s*(.+?)\s*$`)
	// pcsTrailerRe matches the same decision as a commit message line, e.g. "pcs: covered-by
	// traffic.http.retries". Git strips '#' lines from commit messages, hence no comment marker.
	pcsTrailerRe = regexp.MustCompile(`(?m)^\s*pcs:\s*(.+?)\s*$`)
)

// translatorGoldenDirs hold the golden outputs of istiod's Kubernetes config translators (Gateway
// API, the gateway deployment controller, Ingress). A changed golden file means the config istiod
// generates for proxies changed, whether or not the PR author thought of it as a behavior change,
// so TestTranslatorGoldensTracked asks for a coverage decision. Every testdata directory under
// pilot/pkg/config/kube must be listed (or the test fails), so a new translator is not missed.
var translatorGoldenDirs = []string{
	"pilot/pkg/config/kube/gateway/testdata",
	"pilot/pkg/config/kube/gatewaycommon/testdata",
	"pilot/pkg/config/kube/ingress/testdata",
}

// translatorRoot is searched for testdata directories missing from translatorGoldenDirs.
const translatorRoot = "pilot/pkg/config/kube"

// proxyAreas are the release-note areas in which a change can alter what a proxy does. Notes in
// other areas (installation, istioctl, documentation, ...) do not gate the suite: they cannot change
// dataplane behavior.
var proxyAreas = map[string]bool{
	"traffic-management": true,
	"security":           true,
	"networking":         true,
	"network":            true,
	"telemetry":          true,
	"extensibility":      true,
	"EnvoyFilter":        true,
	"pilot":              true,
}

// TestReleaseNotesTracked requires every release note that describes proxy behavior to record the
// suite's coverage decision in the note itself. Release notes are Istio's own declaration that a
// change is user-facing; a note in a proxy area means the conformance suite's view of the world may
// have changed, so the author states the outcome next to the note:
//
//	# pcs: feature <feature-id>        the PR adds this conformance feature
//	# pcs: covered-by <feature-id>     existing behavior, already covered by that feature
//	# pcs: not-proxy-behavior: <why>   cannot change dataplane behavior (reason required)
//
// The referenced feature must exist. The decision lives in the note, so it is reviewable there and
// cannot be satisfied by touching an unrelated file; it is also not duplicated for notes the PR
// does not change.
func TestReleaseNotesTracked(t *testing.T) {
	base := mergeBase(t)
	if base == "" {
		t.Skip("cannot determine the PR's base commit (not a git checkout with origin/master, and no PULL_BASE_SHA); skipping the release-note gate")
	}

	errs, _ := proxyNoteDecisions(t, base, featureRefs(t))
	if len(errs) == 0 {
		return
	}
	sort.Strings(errs)
	t.Errorf(`release notes in proxy areas need a coverage decision in the note:
  %s

Add one of these comments to the note:
  # pcs: feature <feature-id>        this PR adds that conformance feature
  # pcs: covered-by <feature-id>     the behavior is already covered by that feature
  # pcs: not-proxy-behavior: <why>   the change cannot affect a proxy (reason required)`, strings.Join(errs, "\n  "))
}

// TestTranslatorGoldensTracked treats a changed translator golden file (translatorGoldenDirs) as a
// signal that proxy-facing config changed, even when the PR has no release note in a proxy area. Such
// a PR must record a coverage decision either in a changed proxy-area release note (as
// TestReleaseNotesTracked requires anyway) or, for changes without a note, in a commit message line
// between the base and HEAD:
//
//	pcs: feature <feature-id>
//	pcs: covered-by <feature-id>
//	pcs: not-proxy-behavior: <why>
//
// Decisions are validated like release-note decisions: the feature must exist (a former ID listed
// in Aliases is accepted) and not-proxy-behavior needs a reason.
func TestTranslatorGoldensTracked(t *testing.T) {
	// The directory list is checked without git, so it stays current in every environment.
	var found []string
	err := filepath.WalkDir(filepath.Join(env.IstioSrc, translatorRoot), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "testdata" {
			rel, err := filepath.Rel(env.IstioSrc, path)
			if err != nil {
				return err
			}
			found = append(found, filepath.ToSlash(rel))
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range found {
		if !contains(translatorGoldenDirs, d) {
			t.Errorf("translator testdata %s is not in translatorGoldenDirs (coverage/releasenotes_test.go): add it, so its golden changes require a coverage decision", d)
		}
	}
	for _, d := range translatorGoldenDirs {
		if !contains(found, d) {
			t.Errorf("translatorGoldenDirs lists %s, which does not exist: update the list", d)
		}
	}

	base := mergeBase(t)
	if base == "" {
		t.Skip("cannot determine the PR's base commit (not a git checkout with origin/master, and no PULL_BASE_SHA); skipping the golden-file gate")
	}
	var changed []string
	for _, d := range translatorGoldenDirs {
		for _, f := range changedFiles(t, base, d) {
			changed = append(changed, f.path)
		}
	}
	if len(changed) == 0 {
		return
	}

	refs := featureRefs(t)
	noteErrs, decided := proxyNoteDecisions(t, base, refs)
	satisfied := decided > 0 && len(noteErrs) == 0

	var errs []string
	for _, c := range commitMessages(t, base) {
		d, err := parseDecision(c.subject, c.body, pcsTrailerRe)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if len(d.features) == 0 && d.exempt == "" {
			continue // commit records no decision
		}
		if verrs := validateDecision(c.subject, d, refs); len(verrs) > 0 {
			errs = append(errs, verrs...)
			continue
		}
		satisfied = true
	}
	for _, e := range errs {
		t.Errorf("invalid commit decision: %s", e)
	}
	if satisfied {
		return
	}
	sort.Strings(changed)
	if len(changed) > 10 {
		changed = append(changed[:10], fmt.Sprintf("... and %d more", len(changed)-10))
	}
	t.Errorf(`translator golden files changed, so the config proxies receive may have changed:
  %s

Record the proxy conformance decision in a changed release note in a proxy area ("# pcs: ..."), or
add one of these lines to a commit message of this PR:
  pcs: feature <feature-id>        this PR adds that conformance feature
  pcs: covered-by <feature-id>     the behavior is already covered by that feature
  pcs: not-proxy-behavior: <why>   the golden change cannot affect a proxy (reason required)`, strings.Join(changed, "\n  "))
}

// featureRefs returns every ID a decision may reference: the live feature IDs and their aliases.
func featureRefs(t *testing.T) map[string]bool {
	t.Helper()
	features, _ := readFeatures(t)
	refs := map[string]bool{}
	for _, f := range features {
		refs[f.ID] = true
		for _, a := range f.Aliases {
			refs[a] = true
		}
	}
	return refs
}

// proxyNoteDecisions checks the decisions of the proxy-area release notes changed since base. It
// returns the problems found and the number of notes carrying a valid decision.
func proxyNoteDecisions(t *testing.T, base string, refs map[string]bool) ([]string, int) {
	t.Helper()
	var errs []string
	decided := 0
	for _, f := range changedFiles(t, base, notesDir) {
		if f.status == "D" || !strings.HasSuffix(f.path, ".yaml") {
			continue
		}
		area := noteArea(t, f.path)
		if area == "" || !proxyAreas[area] {
			continue
		}
		b, err := os.ReadFile(filepath.Join(env.IstioSrc, f.path))
		if err != nil {
			t.Fatalf("reading %s: %v", f.path, err)
		}
		d, err := parseDecision(f.path, string(b), pcsRe)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		verrs := validateDecision(fmt.Sprintf("%s (area %s)", f.path, area), d, refs)
		if len(verrs) == 0 {
			decided++
		}
		errs = append(errs, verrs...)
	}
	return errs, decided
}

// backlogFile lists the proxy-area release notes written before TestReleaseNotesTracked existed, each
// with its triage decision. New notes record the decision in the note itself instead.
const backlogFile = "releasenote-backlog.yaml"

// releaseNoteBacklog is the checked-in releasenote-backlog.yaml.
type releaseNoteBacklog struct {
	// MaxBacklogTodo is a ratchet: the number of "todo" entries may only go down. -update lowers it;
	// only -raise-todo raises it.
	MaxBacklogTodo int `json:"maxBacklogTodo"`
	// Notes maps a note file name (in releasenotes/notes) to its decision: "todo", or a decision as
	// written after "# pcs:" in a note ("feature <id>", "covered-by <id>", "not-proxy-behavior: <why>").
	Notes map[string]string `json:"notes"`
}

// TestReleaseNoteBacklog triages the proxy-area release notes that predate the in-note gate of
// TestReleaseNotesTracked, without git: every note in a proxy area must either carry a "# pcs:"
// decision itself or be listed in releasenote-backlog.yaml with a decision. It fails on
//   - a proxy-area note with neither (new notes use the in-note decision; -update would add it to
//     the backlog, but as a todo, which the ratchet rejects without -raise-todo);
//   - backlog entries for notes that no longer exist, are not in a proxy area, or carry their own
//     decision (-update removes them);
//   - invalid decisions, in the backlog or in a note;
//   - more "todo" entries than maxBacklogTodo.
//
// -update adds missing notes, pre-classified by classifyNote where that is safe and "todo"
// otherwise, and lowers maxBacklogTodo.
func TestReleaseNoteBacklog(t *testing.T) {
	refs := featureRefs(t)
	_, statErr := os.Stat(backlogFile)
	fresh := os.IsNotExist(statErr)
	bl := readBacklog(t)
	files, err := filepath.Glob(filepath.Join(env.IstioSrc, notesDir, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no release notes found in %s", notesDir)
	}

	var errs []string
	proxyNotes := map[string]bool{}
	added := map[string]string{}
	for _, f := range files {
		name := filepath.Base(f)
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !proxyAreas[noteAreaOf(b)] {
			continue
		}
		proxyNotes[name] = true
		d, err := parseDecision(name, string(b), pcsRe)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		inNote := d.exempt != "" || len(d.features) > 0
		decision, listed := bl.Notes[name]
		switch {
		case inNote:
			errs = append(errs, validateDecision(name, d, refs)...)
			if listed {
				if *update {
					delete(bl.Notes, name)
				} else {
					errs = append(errs, fmt.Sprintf("%s: %s: the note records its own decision; remove the backlog entry (or run with -update)", backlogFile, name))
				}
			}
		case listed:
			errs = append(errs, validateBacklogDecision(name, decision, refs)...)
		case *update:
			added[name] = classifyNote(b)
		default:
			errs = append(errs, fmt.Sprintf("%s: release note in a proxy area without a coverage decision: add \"# pcs: ...\" to the note", name))
		}
	}
	for name := range bl.Notes {
		if proxyNotes[name] {
			continue
		}
		if *update {
			delete(bl.Notes, name)
			continue
		}
		errs = append(errs, fmt.Sprintf("%s: stale entry %q: no such release note in a proxy area; remove it (or run with -update)", backlogFile, name))
	}
	for name, d := range added {
		bl.Notes[name] = d
	}
	todos := 0
	for _, d := range bl.Notes {
		if d == todo {
			todos++
		}
	}
	if *update {
		if fresh || todos < bl.MaxBacklogTodo || *raiseTodo {
			bl.MaxBacklogTodo = todos
		}
		writeBacklog(t, bl)
		t.Logf("updated %s: %d notes (%d added), %d todo, maxBacklogTodo %d", backlogFile, len(bl.Notes), len(added), todos, bl.MaxBacklogTodo)
	}
	if todos > bl.MaxBacklogTodo {
		errs = append(errs, fmt.Sprintf("%s: %d todo entries exceed maxBacklogTodo=%d: new proxy-area notes record their decision in the note (\"# pcs: ...\")",
			backlogFile, todos, bl.MaxBacklogTodo))
	}
	sort.Strings(errs)
	for _, e := range errs {
		t.Error(e)
	}
	t.Logf("release-note backlog: %d proxy-area notes, %d in the backlog, %d todo", len(proxyNotes), len(bl.Notes), todos)
}

// validateBacklogDecision checks a backlog decision: "todo", or a decision in the in-note syntax.
func validateBacklogDecision(name, decision string, refs map[string]bool) []string {
	if decision == todo {
		return nil
	}
	src := backlogFile + ": " + name
	d, err := parseDecision(src, "# pcs: "+decision, pcsRe)
	if err != nil {
		return []string{err.Error()}
	}
	return validateDecision(src, d, refs)
}

var (
	// docsOnlyKinds are note kinds that by definition describe no behavior change.
	docsOnlyKinds = map[string]bool{"test": true, "docs": true, "documentation": true}
	// controlPlaneTopics name the topics that only concern the control plane or its installation.
	// A note is pre-classified by one only if its text matches no proxyTopics word.
	controlPlaneTopics = []struct {
		name string
		re   *regexp.Regexp
	}{
		{"istioctl", regexp.MustCompile(`\bistioctl\b`)},
		{"helm values", regexp.MustCompile(`\bhelm\b|\bvalues\.yaml\b|\bvalues\.[a-z]|--set\b|\bcharts?\b`)},
		{"installation", regexp.MustCompile(`\binstall(s|ed|ing|ation|er)?\b|\bistiooperator\b`)},
	}
	// proxyTopics veto pre-classification: any of them means the note may describe proxy behavior,
	// so it stays a todo for a person to triage. Deliberately broad.
	proxyTopics = regexp.MustCompile(`\b(prox(y|ies)|envoy|sidecars?|gateways?|waypoints?|ztunnel|traffic|rout(e|es|ing|er)|listeners?|` +
		`clusters?|endpoints?|mtls|tls|certs?|certificates?|headers?|requests?|connections?|xds|[a-z]*filters?|polic(y|ies)|` +
		`authori[sz]ation|authenticat|jwt|virtualservices?|destinationrules?|serviceentr|workloadentr|workloadgroup|telemetry|` +
		`metrics?|trac(e|es|ing)|logs?|logging|access.?log|dns|ports?|protocols?|https?|http/?[123]|grpc|tcp|udp|timeouts?|` +
		`retr(y|ies)|redirects?|captur|iptables|nftables|cni|inject|annotations?|labels?|mesh|ambient|wasm|plugins?|lua|` +
		`extensions?|networks?|ip|ipv[46]|dual.?stack|load.?balanc|locality|outlier|circuit|health|probes?|meshconfig|` +
		`proxyconfig|bootstrap|identity|spiffe|trust|sni|alpn|h2|websocket|upstream|downstream|ingress|egress)\b`)
)

// classifyNote returns the backlog decision for an untriaged note: not-proxy-behavior only when it
// is safe to decide without reading the note (a docs/test-only kind, or text that matches only
// control-plane topics), otherwise todo.
func classifyNote(note []byte) string {
	var raw map[string]any
	if err := yamlv3.Unmarshal(note, &raw); err != nil {
		return todo
	}
	if kind, _ := raw["kind"].(string); docsOnlyKinds[strings.TrimSpace(kind)] {
		return fmt.Sprintf("not-proxy-behavior: auto-triaged: note kind %q", strings.TrimSpace(kind))
	}
	var text []string
	var collect func(v any)
	collect = func(v any) {
		switch x := v.(type) {
		case string:
			text = append(text, x)
		case []any:
			for _, e := range x {
				collect(e)
			}
		case map[string]any:
			for _, e := range x {
				collect(e)
			}
		}
	}
	for _, k := range []string{"releaseNotes", "upgradeNotes", "securityNotes"} {
		collect(raw[k])
	}
	body := strings.ToLower(strings.Join(text, "\n"))
	if body == "" || proxyTopics.MatchString(body) {
		return todo
	}
	var matched []string
	for _, topic := range controlPlaneTopics {
		if topic.re.MatchString(body) {
			matched = append(matched, topic.name)
		}
	}
	if len(matched) == 0 {
		return todo
	}
	return fmt.Sprintf("not-proxy-behavior: auto-triaged: only control-plane topics (%s)", strings.Join(matched, ", "))
}

func readBacklog(t *testing.T) releaseNoteBacklog {
	t.Helper()
	bl := releaseNoteBacklog{Notes: map[string]string{}}
	b, err := os.ReadFile(backlogFile)
	if os.IsNotExist(err) && *update {
		return bl
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.UnmarshalStrict(b, &bl); err != nil {
		t.Fatalf("%s: %v", backlogFile, err)
	}
	if bl.Notes == nil {
		bl.Notes = map[string]string{}
	}
	return bl
}

func writeBacklog(t *testing.T, bl releaseNoteBacklog) {
	t.Helper()
	b, err := yaml.Marshal(bl)
	if err != nil {
		t.Fatal(err)
	}
	header := `# Proxy-area release notes written before the release-note gate existed, with their proxy conformance
# triage decision. Checked by TestReleaseNoteBacklog (go test ./tests/integration/conformance/proxy/coverage/).
#
# - Decisions use the syntax of "# pcs:" lines in notes: "feature <id>", "covered-by <id>",
#   "not-proxy-behavior: <why>", or "todo" for an untriaged note. "auto-triaged" reasons were assigned
#   by classifyNote (releasenotes_test.go); review them like any other decision.
# - maxBacklogTodo is a ratchet: it may only go down (-update lowers it; -raise-todo raises it).
# - New notes are never added here by hand: they record the decision in the note itself.
# - Run with -update to drop entries for removed notes or notes that now carry their own decision.
`
	if err := os.WriteFile(backlogFile, append([]byte(header), b...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// noteDecision is the decision recorded in a note's "# pcs:" comments or a commit's "pcs:" lines.
type noteDecision struct {
	features []string
	exempt   string
}

// parseDecision reads the decisions matched by re (pcsRe for notes, pcsTrailerRe for commit
// messages) from text. src names the text in errors.
func parseDecision(src, text string, re *regexp.Regexp) (noteDecision, error) {
	var d noteDecision
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		directive := strings.TrimSpace(m[1])
		switch {
		case strings.HasPrefix(directive, "not-proxy-behavior:"):
			reason := strings.TrimSpace(strings.TrimPrefix(directive, "not-proxy-behavior:"))
			if reason == "" {
				return d, fmt.Errorf("%s: not-proxy-behavior needs a reason", src)
			}
			d.exempt = reason
		case strings.HasPrefix(directive, "feature "), strings.HasPrefix(directive, "covered-by "):
			id := strings.TrimSpace(directive[strings.IndexByte(directive, ' ')+1:])
			if id == "" {
				return d, fmt.Errorf("%s: %q needs a feature ID", src, strings.Fields(directive)[0])
			}
			d.features = append(d.features, id)
		default:
			return d, fmt.Errorf("%s: unknown decision %q (want feature, covered-by or not-proxy-behavior)", src, directive)
		}
	}
	return d, nil
}

// validateDecision checks that a decision was made, is unambiguous, and references existing
// features (refs: live IDs and aliases).
func validateDecision(src string, d noteDecision, refs map[string]bool) []string {
	switch {
	case d.exempt == "" && len(d.features) == 0:
		return []string{fmt.Sprintf("%s: no coverage decision", src)}
	case d.exempt != "" && len(d.features) > 0:
		return []string{fmt.Sprintf("%s: both %q and a feature are recorded; use one", src, "not-proxy-behavior")}
	}
	var errs []string
	for _, id := range d.features {
		if !refs[id] {
			errs = append(errs, fmt.Sprintf("%s: unknown conformance feature %q", src, id))
		}
	}
	return errs
}

type commitMessage struct {
	subject string // "<short sha> <subject>", for messages
	body    string // the full message
}

// commitMessages returns the messages of the commits in base..HEAD.
func commitMessages(t *testing.T, base string) []commitMessage {
	t.Helper()
	out, err := git(t, "log", "--format=%h %s%x1f%B%x1e", base+"..HEAD")
	if err != nil {
		t.Fatalf("git log %s..HEAD: %v", base, err)
	}
	var msgs []commitMessage
	for _, rec := range strings.Split(out, "\x1e") {
		subject, body, ok := strings.Cut(strings.TrimSpace(rec), "\x1f")
		if !ok {
			continue
		}
		msgs = append(msgs, commitMessage{subject: "commit " + subject, body: body})
	}
	return msgs
}

// noteArea returns the note's area field, or "" if it has none.
func noteArea(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(env.IstioSrc, path))
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return noteAreaOf(b)
}

// noteAreaOf returns the area field of a note's content, or "" if it has none.
func noteAreaOf(b []byte) string {
	if m := areaRe.FindSubmatch(b); m != nil {
		return strings.Trim(string(m[1]), `"'`)
	}
	return ""
}

type changedFile struct {
	status string // A, M, D, ...
	path   string
}

// changedFiles lists the files under pathspec that differ between base's merge base with HEAD and
// the working tree, so uncommitted changes are checked during development and a merged base branch
// does not count as this PR's changes.
func changedFiles(t *testing.T, base, pathspec string) []changedFile {
	t.Helper()
	out, err := git(t, "diff", "--name-status", "--no-renames", base, "--", pathspec)
	if err != nil {
		t.Fatalf("git diff %s: %v", base, err)
	}
	var files []changedFile
	for _, line := range strings.Split(out, "\n") {
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			continue
		}
		files = append(files, changedFile{status: fields[0], path: fields[1]})
	}
	return files
}

// mergeBase returns the commit the branch forked from: PULL_BASE_SHA (Prow) or GITHUB_BASE_SHA when
// set, otherwise the closest merge base with master or origin/master, so a stale local branch ref
// does not count merged history as this branch's changes. With no base ref it falls back to HEAD, so
// a local run still checks uncommitted changes. Empty when git cannot answer.
func mergeBase(t *testing.T) string {
	t.Helper()
	head, err := git(t, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	for _, e := range []string{"PULL_BASE_SHA", "GITHUB_BASE_SHA"} {
		if v := os.Getenv(e); v != "" {
			if mb := mergeBaseOf(t, head, v); mb != "" {
				return mb
			}
		}
	}
	best, bestAhead := "", -1
	for _, ref := range []string{"master", "origin/master"} {
		mb := mergeBaseOf(t, head, ref)
		if mb == "" {
			continue
		}
		ahead := commitsBetween(t, mb, head)
		if best == "" || ahead < bestAhead {
			best, bestAhead = mb, ahead
		}
	}
	if best != "" {
		return best
	}
	return head
}

func mergeBaseOf(t *testing.T, head, ref string) string {
	t.Helper()
	if _, err := git(t, "rev-parse", "--verify", ref); err != nil {
		return ""
	}
	out, err := git(t, "merge-base", head, ref)
	if err != nil || out == "" {
		return ""
	}
	return out
}

func commitsBetween(t *testing.T, from, to string) int {
	t.Helper()
	out, err := git(t, "rev-list", "--count", from+".."+to)
	if err != nil {
		return 0
	}
	n := 0
	_, _ = fmt.Sscanf(out, "%d", &n)
	return n
}

func git(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", env.IstioSrc}, args...)...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// TestParseDecision pins the decision syntax of release notes ("# pcs:" comments) and commit messages
// ("pcs:" lines), and the validation both share, without git or the suite's sources.
func TestParseDecision(t *testing.T) {
	refs := map[string]bool{"traffic.http.retries": true, "traffic.retries": true} // live ID and an alias
	cases := []struct {
		name    string
		re      *regexp.Regexp // nil: a releasenote-backlog.yaml decision
		text    string
		wantErr string // substring of the parse or validation error; "" means valid
	}{
		{"note feature", pcsRe, "kind: feature\n# pcs: feature traffic.http.retries\n", ""},
		{"note covered-by alias", pcsRe, "# pcs: covered-by traffic.retries\n", ""},
		{"note exempt", pcsRe, "  # pcs: not-proxy-behavior: istioctl output only\n", ""},
		{"note exempt without reason", pcsRe, "# pcs: not-proxy-behavior:\n", "needs a reason"},
		{"note unknown directive", pcsRe, "# pcs: maybe later\n", "unknown decision"},
		{"note unknown feature", pcsRe, "# pcs: feature traffic.nope\n", "unknown conformance feature"},
		{"note both", pcsRe, "# pcs: feature traffic.http.retries\n# pcs: not-proxy-behavior: x\n", "both"},
		{"note none", pcsRe, "kind: bug-fix\n", "no coverage decision"},
		{"note ignores commit syntax", pcsRe, "pcs: feature traffic.http.retries\n", "no coverage decision"},
		{"commit feature", pcsTrailerRe, "gateway: fix retries\n\npcs: feature traffic.http.retries\n", ""},
		{"commit covered-by alias", pcsTrailerRe, "fix\n\nSigned-off-by: a\npcs: covered-by traffic.retries", ""},
		{"commit exempt", pcsTrailerRe, "fix\n\npcs: not-proxy-behavior: status condition wording only\n", ""},
		{"commit exempt without reason", pcsTrailerRe, "fix\n\npcs: not-proxy-behavior:\n", "needs a reason"},
		{"commit unknown feature", pcsTrailerRe, "fix\n\npcs: covered-by traffic.nope\n", "unknown conformance feature"},
		{"commit mention mid-line is not a decision", pcsTrailerRe, "fix, see pcs: feature traffic.http.retries\n", "no coverage decision"},
		{"commit missing ID", pcsTrailerRe, "fix\n\npcs: feature  \n", "unknown decision"},
		{"backlog todo", nil, "todo", ""},
		{"backlog covered-by alias", nil, "covered-by traffic.retries", ""},
		{"backlog exempt", nil, "not-proxy-behavior: auto-triaged: only control-plane topics (istioctl)", ""},
		{"backlog exempt without reason", nil, "not-proxy-behavior:", "needs a reason"},
		{"backlog unknown feature", nil, "feature traffic.nope", "unknown conformance feature"},
		{"backlog unknown directive", nil, "later", "unknown decision"},
		{"backlog empty", nil, "", "unknown decision"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var errs []string
			if tc.re == nil {
				errs = validateBacklogDecision("src", tc.text, refs)
			} else if d, err := parseDecision("src", tc.text, tc.re); err != nil {
				errs = append(errs, err.Error())
			} else {
				errs = validateDecision("src", d, refs)
			}
			got := strings.Join(errs, "; ")
			switch {
			case tc.wantErr == "" && got != "":
				t.Fatalf("want valid, got %q", got)
			case tc.wantErr != "" && !strings.Contains(got, tc.wantErr):
				t.Fatalf("want error containing %q, got %q", tc.wantErr, got)
			}
		})
	}
}

// TestClassifyNote pins the backlog pre-classification: only docs/test kinds and notes whose text is
// purely about control-plane topics are decided automatically; anything that may touch a proxy stays
// a todo.
func TestClassifyNote(t *testing.T) {
	note := func(kind, text string) []byte {
		return []byte("apiVersion: release-notes/v2\nkind: " + kind + "\narea: traffic-management\n" +
			"# - installation and istioctl appear in the template comments\nreleaseNotes:\n- |\n  " + text + "\n")
	}
	cases := []struct {
		name string
		note []byte
		want string // prefix of the decision
	}{
		{"test kind", note("test", "**Added** tests for retries."), "not-proxy-behavior: auto-triaged: note kind \"test\""},
		{"istioctl only", note("feature", "**Added** `istioctl x precheck` output."), "not-proxy-behavior: auto-triaged: only control-plane topics (istioctl)"},
		{"helm values only", note("feature", "**Added** a value to the istiod Helm chart."), "not-proxy-behavior: auto-triaged: only control-plane topics (helm values)"},
		{"istioctl and proxy", note("bug-fix", "**Fixed** `istioctl proxy-config` and the sidecar listener."), todo},
		{"helm value changing the proxy", note("feature", "**Added** `values.global.proxy.holdApplicationUntilProxyStarts`."), todo},
		{"no control-plane topic", note("bug-fix", "**Fixed** a rare race condition."), todo},
		{"comments do not count", note("bug-fix", "**Fixed** a typo."), todo},
		{"upgrade notes count", []byte("kind: feature\narea: security\nupgradeNotes:\n- title: x\n  content: the TLS default changed\n" +
			"releaseNotes:\n- '**Removed** an istioctl flag.'\n"), todo},
		{"invalid yaml", []byte("kind: [\n"), todo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyNote(tc.note); !strings.HasPrefix(got, tc.want) {
				t.Fatalf("classifyNote = %q, want prefix %q", got, tc.want)
			}
		})
	}
}
