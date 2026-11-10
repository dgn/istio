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

// Package outcome holds the cluster-independent parts of the proxy conformance suite (PCS): the
// outcome taxonomy, the comparison of observed against declared support, the statistics used by
// probabilistic checks, and the report format. It has no build tag so that its logic is unit tested
// in `make test`; the integration harness (package proxy, tag integ) only feeds it observations.
package outcome

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// Outcome is how a proxy handles a feature. It distinguishes *how* a feature is unsupported, because
// that is what users experience: a rejected config is visible, an ignored one is not.
type Outcome string

const (
	// Supported: every behavioral check passed.
	Supported Outcome = "supported"
	// Partial: some subcases (Scenario.Case) passed, others failed.
	Partial Outcome = "partial"
	// Rejected: the config was visibly refused, by a webhook or a False status condition.
	Rejected Outcome = "rejected"
	// FailClosed: config accepted silently, but traffic that should pass was denied. Only classified
	// for security features, where it is the safe failure mode.
	FailClosed Outcome = "fail-closed"
	// Fail: wrong behavior of another kind.
	Fail Outcome = "fail"
	// Ignored: config accepted, no status, and the feature fails exactly as it does with its config
	// removed. Always a hard failure for security features unless declared with an issue.
	Ignored Outcome = "ignored"
	// Indeterminate: the run could not establish whether the config was applied (e.g. a propagation
	// witness never appeared). Not a conformance result.
	Indeterminate Outcome = "indeterminate"
	// Flaky: a failed run passed when repeated. Not a conformance result; the test must be fixed.
	Flaky Outcome = "flaky"
	// Skipped: the feature skipped itself (environment does not allow it to run).
	Skipped Outcome = "skipped"
)

// Declared statuses that are not outcomes.
const (
	// Unsupported is a declaration matching any outcome other than Supported.
	Unsupported = "unsupported"
	// Unknown is a declaration that nobody has measured the feature yet.
	Unknown = "unknown"
)

// rank orders conclusive outcomes from best to worst. An observation ranked lower than the
// declaration is a regression; ranked higher, the declaration is stale.
var rank = map[Outcome]int{
	Supported:  5,
	Partial:    4,
	Rejected:   3,
	FailClosed: 2,
	Fail:       1,
	Ignored:    0,
}

// Conclusive reports whether the outcome is a conformance result (as opposed to a test problem).
func (o Outcome) Conclusive() bool {
	_, ok := rank[o]
	return ok
}

// Observed is the classified result of one (proxy, feature) run.
type Observed struct {
	Outcome Outcome `json:"outcome"`
	// Reason is the first failure message, or why the outcome was classified as it was.
	Reason string `json:"reason,omitempty"`
	// FailedAt is the source location (file:line in a features_*_test.go file) of the first failed
	// assertion. Comparing it between a run and its negative control is what detects Ignored.
	FailedAt string `json:"failedAt,omitempty"`
	// PassedCases and FailedCases list the subcases, if the feature has any.
	PassedCases []string `json:"passedCases,omitempty"`
	FailedCases []string `json:"failedCases,omitempty"`
}

// Classification is the evidence collected for a failed run, from which Classify derives the outcome.
type Classification struct {
	// Rejection is non-empty when the config was refused (webhook error or False condition).
	Rejection string
	// Indeterminate is non-empty when the feature could not establish propagation.
	Indeterminate string
	// ControlFailedAt is where the negative control (same feature, config removed) failed; empty when
	// the control passed or was not applicable.
	ControlFailedAt string
	// Denied is true when the failing call was answered with a denial (401/403 or reset).
	Denied bool
	// Security marks security features, for which FailClosed is classified.
	Security bool
}

// Classify derives the outcome of a failed run. The order matters: a visible rejection explains
// everything after it; passing subcases prove the config had an effect, so it is not Ignored; a
// failure identical to the control's means the config had no effect at all.
func Classify(o Observed, c Classification) Observed {
	switch {
	case c.Rejection != "":
		o.Outcome, o.Reason = Rejected, c.Rejection
	case len(o.PassedCases) > 0 && len(o.FailedCases) > 0:
		o.Outcome = Partial
	case o.FailedAt != "" && o.FailedAt == c.ControlFailedAt:
		o.Outcome = Ignored
		o.Reason = "fails exactly as with the feature config removed: " + o.Reason
	case c.Indeterminate != "":
		o.Outcome, o.Reason = Indeterminate, c.Indeterminate
	case c.Security && c.Denied:
		o.Outcome = FailClosed
	default:
		o.Outcome = Fail
	}
	return o
}

// DecisionKind is what the harness does with a classified result.
type DecisionKind string

const (
	// Pass: observed matches a declared Supported.
	Pass DecisionKind = "pass"
	// KnownGap: observed matches a declared gap; the subtest is skipped, not failed.
	KnownGap DecisionKind = "known-gap"
	// Undeclared: nobody declared support yet; reported and skipped.
	Undeclared DecisionKind = "undeclared"
	// Regression: observed is worse than declared.
	Regression DecisionKind = "regression"
	// Stale: observed is better than declared; the manifest must be updated.
	Stale DecisionKind = "stale"
	// Invalid: the result cannot be accepted whatever is declared (flaky, indeterminate, or a
	// security feature silently ignored without an acknowledged issue).
	Invalid DecisionKind = "invalid"
)

// Fails reports whether the decision fails the subtest.
func (k DecisionKind) Fails() bool {
	return k == Regression || k == Stale || k == Invalid
}

// Decision is the verdict for one result, with a message for the test log.
type Decision struct {
	Kind    DecisionKind `json:"kind"`
	Message string       `json:"message"`
}

// Declaration is the declared support for a feature (see coverage.Declared).
type Declaration struct {
	Status string
	Issue  string
	// Cases are the subcases declared to pass, for a Partial declaration.
	Cases []string
}

// Decide compares an observation with the declaration. security marks features whose silent failure
// is a vulnerability (ID prefix "security.").
func Decide(security bool, obs Observed, d Declaration) Decision {
	if obs.Outcome == Skipped {
		return Decision{KnownGap, "skipped: " + obs.Reason}
	}
	if !obs.Outcome.Conclusive() {
		return Decision{Invalid, fmt.Sprintf("%s: %s", obs.Outcome, obs.Reason)}
	}
	if security && obs.Outcome == Ignored && !(d.Status == string(Ignored) && d.Issue != "") {
		return Decision{Invalid, "security feature silently ignored (fails open); this is only accepted when " +
			"declared as 'ignored' with an issue: " + obs.Reason}
	}
	switch d.Status {
	case "", Unknown:
		return Decision{Undeclared, fmt.Sprintf("undeclared, observed %s: %s", obs.Outcome, obs.Reason)}
	case Unsupported:
		if obs.Outcome == Supported {
			return Decision{Stale, "declared unsupported but supported: update the manifest"}
		}
		return Decision{KnownGap, fmt.Sprintf("known gap (%s): %s", obs.Outcome, obs.Reason)}
	}
	want := Outcome(d.Status)
	if !want.Conclusive() {
		return Decision{Invalid, fmt.Sprintf("invalid declared status %q", d.Status)}
	}
	got := obs.Outcome
	switch {
	case rank[got] < rank[want]:
		return Decision{Regression, fmt.Sprintf("regression: declared %s, observed %s: %s", want, got, obs.Reason)}
	case rank[got] > rank[want]:
		return Decision{Stale, fmt.Sprintf("better than declared (%s, observed %s): update the manifest", want, got)}
	case got == Supported:
		return Decision{Pass, ""}
	case got == Partial:
		return decidePartial(obs, d)
	}
	return Decision{KnownGap, fmt.Sprintf("known gap (%s): %s", got, obs.Reason)}
}

// decidePartial compares passing subcases: losing a declared case is a regression, gaining one makes
// the declaration stale.
func decidePartial(obs Observed, d Declaration) Decision {
	var lost, gained []string
	for _, c := range d.Cases {
		if !slices.Contains(obs.PassedCases, c) {
			lost = append(lost, c)
		}
	}
	for _, c := range obs.PassedCases {
		if !slices.Contains(d.Cases, c) {
			gained = append(gained, c)
		}
	}
	switch {
	case len(lost) > 0:
		return Decision{Regression, "regression: declared passing cases now fail: " + strings.Join(lost, ", ")}
	case len(gained) > 0:
		return Decision{Stale, "more cases pass than declared (" + strings.Join(gained, ", ") + "): update the manifest"}
	}
	return Decision{KnownGap, "known gap (partial), failing cases: " + strings.Join(obs.FailedCases, ", ")}
}

// Statistics for probabilistic checks (weighted routing, fault percentages).
//
// Tolerances are derived from the sample size and a target false-failure rate instead of being
// picked per test: a hand-picked "+/- 12" on 100 requests is a ~3 sigma bound, which fails about once
// in 370 runs, and the suite runs hundreds of (proxy, feature) pairs per CI job.
const (
	// Z is the two-sided z-score for a false-failure probability of 1e-6 per checked share.
	Z = 4.89
	// Resolution is the largest tolerance (in percentage points) a check may have. SampleSize picks
	// a count that achieves it, so a check still tells 50/50 from 70/30.
	Resolution = 10.0
)

// Tolerance returns the allowed deviation, in percentage points, of the observed share of an
// outcome with expected share pct (0-100) over n samples.
func Tolerance(n, pct int) float64 {
	if n <= 0 {
		return math.Inf(1)
	}
	p := float64(pct) / 100
	return Z * math.Sqrt(p*(1-p)/float64(n)) * 100
}

// SampleSize returns the number of samples needed so that the tolerance of every share is at most
// Resolution points.
func SampleSize(pcts ...int) int {
	n := 1
	for _, pct := range pcts {
		p := float64(pct) / 100
		need := int(math.Ceil(math.Pow(Z*math.Sqrt(p*(1-p))*100/Resolution, 2)))
		n = max(n, need)
	}
	return n
}

// CheckShare verifies that got of n samples is consistent with the expected share pct.
func CheckShare(name string, got, n, pct int) error {
	if need := SampleSize(pct); n < need {
		return fmt.Errorf("sample too small for %q: %d, need %d (use outcome.SampleSize)", name, n, need)
	}
	share := float64(got) * 100 / float64(n)
	if tol := Tolerance(n, pct); math.Abs(share-float64(pct)) > tol {
		return fmt.Errorf("%q got %.1f%% of %d samples, expected %d%% +/- %.1f", name, share, n, pct, tol)
	}
	return nil
}
