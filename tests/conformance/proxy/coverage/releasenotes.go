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
	"regexp"
	"strings"

	yamlv3 "gopkg.in/yaml.v3" // nolint: depguard // YAML 1.2 parsing of notes; sigs.k8s.io/yaml accepts malformed ones (e.g. duplicate keys)
	"sigs.k8s.io/yaml"
)

// Release notes are Istio's own record of user-facing changes. The checks in releasenotes_test.go
// require a coverage decision for every note in a proxy area; this file holds what they share with
// the pcs tool, which triages the notes of past releases.

// BacklogFile, in CoverageDir, lists the proxy-area release notes written before the release-note
// gate existed, each with its triage decision.
const BacklogFile = "releasenote-backlog.yaml"

// BacklogTodo is the decision of an untriaged note.
const BacklogTodo = "todo"

// ReleaseNoteBacklog is the checked-in releasenote-backlog.yaml.
type ReleaseNoteBacklog struct {
	// MaxBacklogTodo is a ratchet: the number of "todo" entries may only go down. -update lowers it;
	// only -raise-todo raises it.
	MaxBacklogTodo int `json:"maxBacklogTodo"`
	// Notes maps a note file name (in releasenotes/notes) to its decision: "todo", or a decision as
	// written after "# pcs:" in a note ("feature <id>", "covered-by <id>", "not-proxy-behavior: <why>").
	Notes map[string]string `json:"notes"`
}

// LoadBacklog reads a ReleaseNoteBacklog. Notes is never nil.
func LoadBacklog(path string) (ReleaseNoteBacklog, error) {
	bl := ReleaseNoteBacklog{Notes: map[string]string{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return bl, err
	}
	if err := yaml.UnmarshalStrict(b, &bl); err != nil {
		return bl, fmt.Errorf("%s: %v", path, err)
	}
	if bl.Notes == nil {
		bl.Notes = map[string]string{}
	}
	return bl, nil
}

// ProxyAreas are the release-note areas in which a change can alter what a proxy does. Notes in
// other areas (installation, istioctl, documentation, ...) do not gate the suite: they cannot change
// dataplane behavior.
var ProxyAreas = map[string]bool{
	"traffic-management": true,
	"security":           true,
	"networking":         true,
	"network":            true,
	"telemetry":          true,
	"extensibility":      true,
	"EnvoyFilter":        true,
	"pilot":              true,
}

var areaRe = regexp.MustCompile(`(?m)^area:\s*(\S+)\s*$`)

// NoteDecisionRe matches the decision comment in a release note, e.g. "# pcs: feature
// egress.tls-origination".
var NoteDecisionRe = regexp.MustCompile(`(?m)^\s*#\s*pcs:\s*(.+?)\s*$`)

// NoteArea returns the area field of a note's content, or "" if it has none.
func NoteArea(b []byte) string {
	if m := areaRe.FindSubmatch(b); m != nil {
		return strings.Trim(string(m[1]), `"'`)
	}
	return ""
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

// NoteText returns the user-facing text of a note (releaseNotes, upgradeNotes, securityNotes), or
// "" if it does not parse.
func NoteText(note []byte) string {
	var raw map[string]any
	if err := yamlv3.Unmarshal(note, &raw); err != nil {
		return ""
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
	return strings.Join(text, "\n")
}

// ClassifyNote returns the backlog decision for an untriaged note: not-proxy-behavior only when it
// is safe to decide without reading the note (a docs/test-only kind, or text that matches only
// control-plane topics), otherwise todo.
func ClassifyNote(note []byte) string {
	var raw map[string]any
	if err := yamlv3.Unmarshal(note, &raw); err != nil {
		return BacklogTodo
	}
	if kind, _ := raw["kind"].(string); docsOnlyKinds[strings.TrimSpace(kind)] {
		return fmt.Sprintf("not-proxy-behavior: auto-triaged: note kind %q", strings.TrimSpace(kind))
	}
	body := strings.ToLower(NoteText(note))
	if body == "" || proxyTopics.MatchString(body) {
		return BacklogTodo
	}
	var matched []string
	for _, topic := range controlPlaneTopics {
		if topic.re.MatchString(body) {
			matched = append(matched, topic.name)
		}
	}
	if len(matched) == 0 {
		return BacklogTodo
	}
	return fmt.Sprintf("not-proxy-behavior: auto-triaged: only control-plane topics (%s)", strings.Join(matched, ", "))
}
