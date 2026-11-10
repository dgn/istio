//go:build integ

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

package proxy

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	"istio.io/istio/pkg/test/env"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/tests/integration/conformance/proxy/coverage"
	"istio.io/istio/tests/integration/conformance/proxy/outcome"
)

// Outcome classification.
//
// A feature's Run is imperative: it calls t.Fatal when an assertion fails. The harness runs it
// against a recorder instead of the real test, so that a failure becomes evidence rather than a
// verdict, and classifies the result (outcome.Classify) from:
//   - where the first assertion failed (file:line in a features_*_test.go file);
//   - whether the config was refused (webhook error, or a False condition on an applied object);
//   - whether the failing call was answered with a denial (for FailClosed);
//   - a rerun (a failure that passes on rerun is Flaky, never a pass);
//   - a negative control on the same proxy, the feature with its config removed: failing at the
//     same assertion means the config had no effect at all, which is Ignored.
// The outcome is then compared with the implementation's declared support (declared/<role>/<impl>.yaml)
// and the subtest passes, is skipped as a known gap, or fails (outcome.Decide).

var (
	classifyFlag = flag.Bool("istio.test.pcs.classify", true,
		"Classify failed features (rerun for flakiness, negative control for Ignored). Disable for a fast "+
			"pass/fail run; failures are then classified from the first run only.")
	updateDeclared = flag.Bool("istio.test.pcs.update-declared", false,
		"Rewrite declared/<role>/<impl>.yaml with the observed outcomes of the features that ran. "+
			"Keeps reason/issue of entries whose status is unchanged. Review the diff.")
	reportDir = flag.String("istio.test.pcs.report-dir", "",
		"Directory for the pcs-report-<role>.yaml conformance reports. Defaults to $ARTIFACTS, else the "+
			"test working directory.")
)

// featureFileRe matches the files whose lines identify an assertion.
var featureFileRe = regexp.MustCompile(`/features_[a-z_]+_test\.go$`)

// recorder is a TestContext that records failures instead of failing the test. Fatal and Skip stop
// the feature goroutine (run executes the feature in its own goroutine for that reason).
type recorder struct {
	framework.TestContext
	mu      sync.Mutex
	msgs    []string
	at      string
	skipped string
}

// run executes fn on its own goroutine, so that Fatal/Skip (runtime.Goexit) end only the feature.
func (r *recorder) run(fn func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	<-done
}

func (r *recorder) record(msg string) {
	r.recordAt(msg, callSite())
}

func (r *recorder) recordAt(msg, at string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, msg)
	if r.at == "" {
		r.at = at
	}
	r.TestContext.Log("assertion failed (recorded) at " + at + ": " + msg)
}

func (r *recorder) failed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.msgs) > 0
}

// firstFailure returns the first failure, shortened for logs and reports.
func (r *recorder) firstFailure() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.msgs) == 0 {
		return ""
	}
	m := strings.Join(strings.Fields(r.msgs[0]), " ")
	if len(m) > 300 {
		m = m[:300] + "..."
	}
	return m
}

func (r *recorder) failedAt() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.at
}

func (r *recorder) Error(args ...any)                 { r.record(fmt.Sprint(args...)) }
func (r *recorder) Errorf(format string, args ...any) { r.record(fmt.Sprintf(format, args...)) }
func (r *recorder) Fail()                             { r.record("Fail") }
func (r *recorder) Failed() bool                      { return r.failed() }
func (r *recorder) Fatal(args ...any)                 { r.record(fmt.Sprint(args...)); runtime.Goexit() }
func (r *recorder) Fatalf(format string, args ...any) {
	r.record(fmt.Sprintf(format, args...))
	runtime.Goexit()
}
func (r *recorder) FailNow() { r.record("FailNow"); runtime.Goexit() }

func (r *recorder) skip(msg string) {
	r.mu.Lock()
	r.skipped = msg
	r.mu.Unlock()
	runtime.Goexit()
}

func (r *recorder) Skip(args ...any)                 { r.skip(fmt.Sprint(args...)) }
func (r *recorder) Skipf(format string, args ...any) { r.skip(fmt.Sprintf(format, args...)) }
func (r *recorder) SkipNow()                         { r.skip("skipped") }

// callSite returns the innermost features_*_test.go frame of the current stack: the assertion that
// failed, independent of how deep in the harness or echo client the failure was reported.
func callSite() string {
	pcs := make([]uintptr, 64)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		if featureFileRe.MatchString(f.File) {
			return fmt.Sprintf("%s:%d", filepath.Base(f.File), f.Line)
		}
		if !more {
			return ""
		}
	}
}

// Case runs one subcase of a feature. A failing case does not stop the others, and the outcome is
// Partial when some cases pass and others fail ("exact match yes, regex no"). Split a feature into
// cases when its assertions test independent sub-behaviors that an implementation may support
// separately; keep one Run when they only make sense together.
func (s *Scenario) Case(t framework.TestContext, name string, fn func(t framework.TestContext)) {
	t.Helper()
	r := &recorder{TestContext: t}
	r.run(func() { fn(r) })
	if r.skipped != "" {
		return
	}
	s.evidence.recordCase(name, !r.failed())
	if !r.failed() {
		return
	}
	msg := fmt.Sprintf("case %s: %s", name, r.firstFailure())
	if p, ok := t.(*recorder); ok {
		p.recordAt(msg, r.failedAt())
		return
	}
	t.Error(msg)
}

// Indeterminate ends the feature: it could not establish whether its config was applied. Witness
// helpers call it when the propagation witness never appears.
func (s *Scenario) Indeterminate(t framework.TestContext, reason string) {
	t.Helper()
	s.evidence.mu.Lock()
	s.evidence.indeterminate = reason
	s.evidence.mu.Unlock()
	t.Fatal("indeterminate: " + reason)
}

// evidence is what a run records for classification.
type evidence struct {
	mu            sync.Mutex
	applied       []appliedRef
	applyErr      string
	denied        bool
	indeterminate string
	passedCases   []string
	failedCases   []string
}

type appliedRef struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
}

var docSep = regexp.MustCompile(`(?m)^---\s*$`)

func (e *evidence) recordApplied(ns, yamlText string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, doc := range docSep.Split(yamlText, -1) {
		var ref appliedRef
		if err := yaml.Unmarshal([]byte(doc), &ref); err != nil || ref.Kind == "" || ref.Metadata.Name == "" {
			continue
		}
		if ref.Metadata.Namespace == "" {
			ref.Metadata.Namespace = ns
		}
		e.applied = append(e.applied, ref)
	}
}

// recordApplyError keeps admission rejections: they are the visible "Rejected" outcome.
func (e *evidence) recordApplyError(err error) {
	msg := err.Error()
	if !strings.Contains(msg, "admission webhook") && !strings.Contains(msg, "denied the request") &&
		!strings.Contains(msg, "is invalid") {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.applyErr == "" {
		e.applyErr = "config rejected at admission: " + msg
	}
}

func (e *evidence) recordCase(name string, passed bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if passed {
		e.passedCases = append(e.passedCases, name)
	} else {
		e.failedCases = append(e.failedCases, name)
	}
}

// observe wraps a checker to record whether the last failing call was answered with a denial.
func (s *Scenario) observe(check echo.Checker) echo.Checker {
	if check == nil {
		return nil
	}
	return func(result echo.CallResult, err error) error {
		cerr := check(result, err)
		s.evidence.mu.Lock()
		s.evidence.denied = cerr != nil && allDenied(result, err)
		s.evidence.mu.Unlock()
		return cerr
	}
}

// allDenied reports whether every response was an authn/authz denial, or the connection was refused
// or reset (the only denial an L4 proxy can express).
func allDenied(result echo.CallResult, err error) bool {
	if len(result.Responses) == 0 {
		return err != nil
	}
	for _, r := range result.Responses {
		if r.Code != "401" && r.Code != "403" {
			return false
		}
	}
	return true
}

// rejectionConditions are the condition types whose False status means the config was refused.
var rejectionConditions = map[string]bool{
	"Accepted": true, "ResolvedRefs": true, "Programmed": true, "Reconciled": true, "WaypointAccepted": true,
}

// statusRejection inspects the status of everything the feature applied, and returns why it was
// refused, if it was. Kubernetes status is part of the black-box contract (§3.2), and a visible
// rejection is the outcome users can act on.
func (s *Scenario) statusRejection(t framework.TestContext) string {
	s.evidence.mu.Lock()
	applied := slices.Clone(s.evidence.applied)
	applyErr := s.evidence.applyErr
	s.evidence.mu.Unlock()
	if applyErr != "" {
		return applyErr
	}
	dyn := t.Clusters().Default().Dynamic()
	for _, ref := range applied {
		gv, err := schema.ParseGroupVersion(ref.APIVersion)
		if err != nil || !(strings.HasSuffix(gv.Group, "istio.io") || strings.HasSuffix(gv.Group, "gateway.networking.k8s.io")) {
			continue
		}
		gvr := gv.WithResource(plural(ref.Kind))
		obj, err := dyn.Resource(gvr).Namespace(ref.Metadata.Namespace).Get(context.Background(), ref.Metadata.Name, metav1.GetOptions{})
		if err != nil {
			continue
		}
		status, _ := obj.Object["status"].(map[string]any)
		if msg := falseCondition(status); msg != "" {
			return fmt.Sprintf("%s %s: %s", ref.Kind, ref.Metadata.Name, msg)
		}
	}
	return ""
}

// falseCondition finds a False rejection condition in status.conditions, status.parents[].conditions
// (routes) or status.ancestors[].conditions (policies), or an Error validation message (Istio APIs).
func falseCondition(status map[string]any) string {
	var sets [][]any
	if c, ok := status["conditions"].([]any); ok {
		sets = append(sets, c)
	}
	for _, key := range []string{"parents", "ancestors"} {
		list, _ := status[key].([]any)
		for _, p := range list {
			if pm, ok := p.(map[string]any); ok {
				if c, ok := pm["conditions"].([]any); ok {
					sets = append(sets, c)
				}
			}
		}
	}
	for _, set := range sets {
		for _, c := range set {
			cm, _ := c.(map[string]any)
			if typ, _ := cm["type"].(string); rejectionConditions[typ] && cm["status"] == "False" {
				return fmt.Sprintf("%s=False (%v: %v)", typ, cm["reason"], cm["message"])
			}
		}
	}
	msgs, _ := status["validationMessages"].([]any)
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		if lvl, _ := mm["level"].(string); strings.EqualFold(lvl, "error") {
			return fmt.Sprintf("validation error: %v", mm["documentation_url"])
		}
	}
	return ""
}

// plural returns the resource name of a Kind (AuthorizationPolicy -> authorizationpolicies).
func plural(kind string) string {
	k := strings.ToLower(kind)
	switch {
	case strings.HasSuffix(k, "y") && !strings.HasSuffix(k, "ay"):
		return k[:len(k)-1] + "ies"
	case strings.HasSuffix(k, "s"):
		return k + "es"
	}
	return k + "s"
}

// attempt is one run of a feature in its own subtest, so its resources are cleaned up before the next
// run reuses the same names.
type attempt struct {
	s *Scenario
	r *recorder
	// rejection is read from status before the attempt's resources are removed.
	rejection string
}

func runAttempt(t framework.TestContext, name string, f Feature, base *Scenario, control bool) attempt {
	var a attempt
	t.NewSubTest(name).Run(func(t framework.TestContext) {
		a.s = &Scenario{Proxy: base.Proxy, Env: pcs, Feature: f, shared: base.shared, waypoint: base.waypoint, control: control}
		a.s.applyMeshWide(t)
		a.r = &recorder{TestContext: t}
		a.r.run(func() { f.Run(a.r, a.s) })
		if a.r.failed() && !control {
			a.rejection = a.s.statusRejection(t)
		}
	})
	return a
}

// observeFeature runs a feature and classifies the result.
func observeFeature(t framework.TestContext, f Feature, base *Scenario) (outcome.Observed, int) {
	first := runAttempt(t, "run", f, base, false)
	ev := &first.s.evidence
	obs := outcome.Observed{
		Reason:      first.r.firstFailure(),
		FailedAt:    first.r.failedAt(),
		PassedCases: ev.passedCases,
		FailedCases: ev.failedCases,
	}
	switch {
	case first.r.skipped != "":
		return outcome.Observed{Outcome: outcome.Skipped, Reason: first.r.skipped}, 1
	case !first.r.failed():
		obs.Outcome = outcome.Supported
		return obs, 1
	}
	c := outcome.Classification{
		Rejection:     first.rejection,
		Indeterminate: ev.indeterminate,
		Denied:        ev.denied,
		Security:      f.security(),
	}
	attempts := 1
	if *classifyFlag {
		rerun := runAttempt(t, "rerun", f, base, false)
		attempts++
		if !rerun.r.failed() && rerun.r.skipped == "" {
			obs.Outcome = outcome.Flaky
			obs.Reason = "failed, then passed on rerun: " + obs.Reason
			return obs, attempts
		}
		if f.NoControl == "" {
			ctl := runAttempt(t, "control", f, base, true)
			attempts++
			if ctl.s.stripped && ctl.r.failed() {
				c.ControlFailedAt = ctl.r.failedAt()
			}
		}
	}
	return outcome.Classify(obs, c), attempts
}

// declarations holds the declared support manifest of one (role, implementation).
type declarations struct {
	path string
	mu   sync.Mutex
	m    coverage.Manifest
}

func suiteDir() string {
	return filepath.Join(env.IstioSrc, "tests/integration/conformance/proxy")
}

func loadDeclarations(t framework.TestContext, role Role, impl string) *declarations {
	path := coverage.ManifestPath(suiteDir(), string(role), impl)
	m, err := coverage.LoadManifest(path)
	if err != nil {
		t.Fatalf("no declared support manifest for %s/%s (%s): %v", role, impl, path, err)
	}
	return &declarations{path: path, m: m}
}

// lookup returns the declaration of a feature, under its ID or a former ID.
func (d *declarations) lookup(f Feature) outcome.Declaration {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, id := range append([]string{f.ID}, f.Aliases...) {
		if decl, ok := d.m.Features[id]; ok {
			return outcome.Declaration{Status: decl.Status, Issue: decl.Issue, Cases: decl.Cases}
		}
	}
	return outcome.Declaration{}
}

// record updates the manifest with an observation (for -istio.test.pcs.update-declared).
func (d *declarations) record(f Feature, obs outcome.Observed) {
	if !obs.Outcome.Conclusive() {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.m.Features == nil {
		d.m.Features = map[string]coverage.Declared{}
	}
	cur := d.m.Features[f.ID]
	status := string(obs.Outcome)
	if cur.Status == status || (cur.Status == coverage.StatusUnsupported && obs.Outcome != outcome.Supported) {
		if obs.Outcome == outcome.Partial {
			cur.Cases = obs.PassedCases
			d.m.Features[f.ID] = cur
		}
		return
	}
	next := coverage.Declared{Status: status}
	if obs.Outcome != outcome.Supported {
		next.Reason = obs.Reason
	}
	if obs.Outcome == outcome.Partial {
		next.Cases = obs.PassedCases
	}
	d.m.Features[f.ID] = next
}

func (d *declarations) write() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.m.Write(d.path)
}

// reports collects results per role; written when the role's test ends.
var (
	reportsMu sync.Mutex
	reports   = map[Role]*outcome.Report{}
)

func addResult(role Role, impl string, r outcome.Result) {
	reportsMu.Lock()
	defer reportsMu.Unlock()
	rep := reports[role]
	if rep == nil {
		rep = &outcome.Report{Role: string(role)}
		reports[role] = rep
	}
	for i := range rep.Implementations {
		if rep.Implementations[i].Name == impl {
			rep.Implementations[i].Results = append(rep.Implementations[i].Results, r)
			return
		}
	}
	rep.Implementations = append(rep.Implementations, outcome.Implementation{Name: impl, Results: []outcome.Result{r}})
}

// writeReport writes the role's report; called from the role test's cleanup, after all features.
func writeReport(t framework.TestContext, role Role) {
	reportsMu.Lock()
	rep := reports[role]
	reportsMu.Unlock()
	if rep == nil {
		return
	}
	dir := *reportDir
	if dir == "" {
		dir = os.Getenv("ARTIFACTS")
	}
	if dir == "" {
		dir = t.Settings().BaseDir
	}
	img := t.Settings().Image
	rep.Date = time.Now().UTC().Format(time.RFC3339)
	rep.IstioVersion = img.Hub + ":" + img.Tag
	path := filepath.Join(dir, "pcs-report-"+string(role)+".yaml")
	if err := rep.Write(path); err != nil {
		t.Errorf("writing conformance report: %v", err)
		return
	}
	t.Logf("conformance report: %s", path)
}

// runFeature observes a feature on one proxy, compares it with the declaration, and passes, skips
// (known gap / undeclared) or fails the subtest.
func runFeature(t framework.TestContext, f Feature, base *Scenario, decl *declarations) {
	start := time.Now()
	obs, attempts := observeFeature(t, f, base)
	d := decl.lookup(f)
	decision := outcome.Decide(f.security(), obs, d)
	addResult(base.Proxy.Role, base.Proxy.Name, outcome.Result{
		Feature:         f.ID,
		Profile:         string(f.profile()),
		Observed:        obs,
		Declared:        d.Status,
		Decision:        decision.Kind,
		Attempts:        attempts,
		DurationSeconds: time.Since(start).Seconds(),
	})
	if *updateDeclared {
		decl.record(f, obs)
	}
	t.Logf("outcome: %s (declared %q) -> %s", obs.Outcome, d.Status, decision.Kind)
	switch {
	case decision.Kind.Fails():
		t.Error(decision.Message)
	case decision.Kind != outcome.Pass:
		t.Skip(decision.Message)
	}
}
