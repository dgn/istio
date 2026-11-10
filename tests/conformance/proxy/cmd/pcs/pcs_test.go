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
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"istio.io/istio/pkg/test/util/assert"
	"istio.io/istio/tests/conformance/proxy/coverage"
)

func (selection) CmpOpts() []cmp.Option { return []cmp.Option{cmp.AllowUnexported(selection{})} }

func (hunk) CmpOpts() []cmp.Option { return []cmp.Option{cmp.AllowUnexported(hunk{})} }

func TestSelectChanged(t *testing.T) {
	dir := coverage.SuiteDir
	features := []coverage.Feature{
		{ID: "a.one", File: "/src/" + dir + "/features_a_test.go", Line: 10, EndLine: 20},
		{ID: "a.two", File: "/src/" + dir + "/features_a_test.go", Line: 22, EndLine: 30},
		{ID: "b.one", File: "/src/" + dir + "/features_b_test.go", Line: 10, EndLine: 20},
	}
	diff := `diff --git a/` + dir + `/features_a_test.go b/` + dir + `/features_a_test.go
--- a/` + dir + `/features_a_test.go
+++ b/` + dir + `/features_a_test.go
@@ -15 +15 @@ func init() {
-old
+new
@@ -30,0 +31,2 @@ func init() {
+added
+added
diff --git a/` + dir + `/features_b_test.go b/` + dir + `/features_b_test.go
--- a/` + dir + `/features_b_test.go
+++ b/` + dir + `/features_b_test.go
@@ -20,1 +19,0 @@
-deleted
`
	hunks := parseDiff(diff)
	assert.Equal(t, hunks, []hunk{
		{file: dir + "/features_a_test.go", start: 15, end: 15},
		{file: dir + "/features_a_test.go", start: 31, end: 32},
		{file: dir + "/features_b_test.go", start: 19, end: 20},
	})
	// a.one: inside its literal. a.two is not touched: lines 31-32 are outside every literal of the
	// file, which selects the whole file. b.one: a deletion at its last line.
	assert.Equal(t, selectChanged(features, hunks), selection{features: []string{"a.one", "a.two", "b.one"}})
	assert.Equal(t, selectChanged(features, hunks[:1]), selection{features: []string{"a.one"}})

	sel := selectChanged(features, []hunk{{file: dir + "/harness_test.go", start: 1, end: 1}})
	assert.Equal(t, sel.all, true)

	deleted := parseDiff("--- a/" + dir + "/features_b_test.go\n+++ /dev/null\n@@ -1,40 +0,0 @@\n")
	assert.Equal(t, selectChanged(features, deleted), selection{features: []string{"b.one"}})
}

func TestRunPattern(t *testing.T) {
	suite := &coverage.Suite{Features: []coverage.Feature{
		{ID: "a.one", Roles: []string{"sidecar", "ingress"}},
		{ID: "b.two", Roles: []string{"ingress"}},
	}}
	got := runPattern(suite, []string{"a.one"}, true)
	assert.Equal(t, got, `^Test(Ingress|Sidecar)Conformance(Controls)?$/.*/^(a\.one)$`)
	// go test splits -run on unbracketed slashes, one pattern per subtest level.
	levels := strings.Split(got, "/")
	for name, want := range map[string]bool{
		"TestSidecarConformance/envoy/a.one":         true,
		"TestIngressConformanceControls/envoy/a.one": true,
		"TestIngressConformance/envoy/aXone":         false,
		"TestEgressConformance/envoy/a.one":          false,
	} {
		parts := strings.Split(name, "/")
		match := true
		for i, p := range parts {
			match = match && regexp.MustCompile(levels[i]).MatchString(p)
		}
		assert.Equal(t, match, want, name)
	}
}

func TestScaffold(t *testing.T) {
	assert.Equal(t, scaffoldID("VirtualService.http.retries.retryOn"), "virtualservice.http-retries-retryon")
	assert.Equal(t, scaffoldID("AuthorizationPolicy.action=AUDIT"), "authorizationpolicy.action-audit")
	assert.Equal(t, scaffoldID("flag:istiod:PILOT_FOO_BAR"), "flag.istiod-pilot-foo-bar")
	src, err := scaffoldSource("waypoint", "", []string{"VirtualService.http.timeout"}, func(string) string { return "line one\nline two" })
	assert.NoError(t, err)
	for _, want := range []string{
		`ID:          "TODO.virtualservice.http-timeout"`,
		`Roles:       []Role{RoleWaypoint}`,
		`"VirtualService.http.timeout",`,
		"//   line one\n\t\t//   line two",
		`t.Fatal("TODO: implement")`,
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("scaffold lacks %q:\n%s", want, src)
		}
	}
}

func TestDocs(t *testing.T) {
	d, err := newDocs()
	assert.NoError(t, err)
	for key, want := range map[string]string{
		// A plain field, a oneof member (generated in a wrapper type), and a nested enum's value.
		"VirtualService.http.timeout":                         "Timeout for HTTP requests",
		"VirtualService.http.match.uri.exact":                 "exact string match",
		"DestinationRule.trafficPolicy.tls.mode=ISTIO_MUTUAL": "ISTIO_MUTUAL: Secure connections to the upstream using mutual TLS",
		// A mesh API field, and an interaction.
		"MeshConfig.accessLogFile":           "File address for the proxy access log",
		"interaction:" + firstInteraction(t): "Roles:",
	} {
		if got := d.of(key); !strings.Contains(got, want) {
			t.Errorf("docs of %s = %q, want it to contain %q", key, got, want)
		}
	}
	for _, key := range []string{"VirtualService.http.timeout", "MeshConfig.accessLogFile"} {
		if strings.Contains(d.of(key), "+kubebuilder") || strings.Contains(d.of(key), "$hide") {
			t.Errorf("docs of %s contain generator directives: %q", key, d.of(key))
		}
	}
}

func firstInteraction(t *testing.T) string {
	is, err := coverage.Interactions()
	assert.NoError(t, err)
	return is[0].ID
}

func TestWorstStatus(t *testing.T) {
	status := map[string]string{"a": coverage.StatusSupported, "b": coverage.StatusIgnored, "c": coverage.StatusPartial}
	assert.Equal(t, worstStatus(nil, status), statusGap)
	assert.Equal(t, worstStatus([]string{"a", "c"}, status), coverage.StatusPartial)
	assert.Equal(t, worstStatus([]string{"a", "b", "c"}, status), coverage.StatusIgnored)
	assert.Equal(t, worstStatus([]string{"a", "missing"}, status), coverage.StatusUnknown)
}

func TestUntriaged(t *testing.T) {
	bl := coverage.ReleaseNoteBacklog{Notes: map[string]string{"old.yaml": "not-proxy-behavior: x", "todo.yaml": coverage.BacklogTodo}}
	note := []byte("apiVersion: release-notes/v2\nkind: bug-fix\narea: traffic-management\nreleaseNotes:\n- Fixed routing.\n")
	_, ok := untriaged("new.yaml", note, bl)
	assert.Equal(t, ok, true)
	_, ok = untriaged("todo.yaml", note, bl)
	assert.Equal(t, ok, true)
	_, ok = untriaged("old.yaml", note, bl)
	assert.Equal(t, ok, false)
	_, ok = untriaged("new.yaml", append([]byte("# pcs: not-proxy-behavior: x\n"), note...), bl)
	assert.Equal(t, ok, false)
	_, ok = untriaged("new.yaml", []byte("kind: bug-fix\narea: istioctl\nreleaseNotes:\n- x\n"), bl)
	assert.Equal(t, ok, false)
}
