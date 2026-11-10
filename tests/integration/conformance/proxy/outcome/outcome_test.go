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

package outcome

import (
	"math"
	"testing"
)

func TestClassify(t *testing.T) {
	failed := Observed{FailedAt: "features_x_test.go:10", Reason: "boom"}
	cases := []struct {
		name string
		obs  Observed
		c    Classification
		want Outcome
	}{
		{"rejection wins", failed, Classification{Rejection: "webhook denied", ControlFailedAt: failed.FailedAt}, Rejected},
		{"some cases pass", Observed{FailedAt: "a", PassedCases: []string{"x"}, FailedCases: []string{"y"}}, Classification{ControlFailedAt: "a"}, Partial},
		{"all cases fail like control", Observed{FailedAt: "a", FailedCases: []string{"x", "y"}}, Classification{ControlFailedAt: "a"}, Ignored},
		{"same failure as control", failed, Classification{ControlFailedAt: failed.FailedAt}, Ignored},
		{"control fails elsewhere", failed, Classification{ControlFailedAt: "features_x_test.go:20"}, Fail},
		{"control not applicable", failed, Classification{}, Fail},
		{"witness missing", failed, Classification{Indeterminate: "no witness"}, Indeterminate},
		{"ignored beats indeterminate", failed, Classification{Indeterminate: "no witness", ControlFailedAt: failed.FailedAt}, Ignored},
		{"security denial", failed, Classification{Security: true, Denied: true}, FailClosed},
		{"non-security denial", failed, Classification{Denied: true}, Fail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.obs, tc.c).Outcome; got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestDecide(t *testing.T) {
	obs := func(o Outcome, passed ...string) Observed { return Observed{Outcome: o, PassedCases: passed} }
	cases := []struct {
		name     string
		security bool
		obs      Observed
		decl     Declaration
		want     DecisionKind
	}{
		{"supported as declared", false, obs(Supported), Declaration{Status: "supported"}, Pass},
		{"regression", false, obs(Fail), Declaration{Status: "supported"}, Regression},
		{"worse gap", false, obs(Ignored), Declaration{Status: "rejected"}, Regression},
		{"better gap", false, obs(Rejected), Declaration{Status: "ignored"}, Stale},
		{"now supported", false, obs(Supported), Declaration{Status: "fail"}, Stale},
		{"same gap", false, obs(Rejected), Declaration{Status: "rejected"}, KnownGap},
		{"unsupported matches any gap", false, obs(Fail), Declaration{Status: Unsupported}, KnownGap},
		{"unsupported but supported", false, obs(Supported), Declaration{Status: Unsupported}, Stale},
		{"unknown", false, obs(Fail), Declaration{Status: Unknown}, Undeclared},
		{"missing", false, obs(Supported), Declaration{}, Undeclared},
		{"flaky never accepted", false, obs(Flaky), Declaration{Status: "supported"}, Invalid},
		{"indeterminate never accepted", false, obs(Indeterminate), Declaration{Status: Unsupported}, Invalid},
		{"skipped", false, obs(Skipped), Declaration{Status: "supported"}, KnownGap},
		{"security ignored undeclared", true, obs(Ignored), Declaration{}, Invalid},
		{"security ignored as unsupported", true, obs(Ignored), Declaration{Status: Unsupported}, Invalid},
		{"security ignored without issue", true, obs(Ignored), Declaration{Status: "ignored"}, Invalid},
		{"security ignored acknowledged", true, obs(Ignored), Declaration{Status: "ignored", Issue: "istio/istio#1"}, KnownGap},
		{"security fail-closed declared", true, obs(FailClosed), Declaration{Status: "fail-closed"}, KnownGap},
		{"partial same cases", false, obs(Partial, "a"), Declaration{Status: "partial", Cases: []string{"a"}}, KnownGap},
		{"partial lost case", false, obs(Partial, "a"), Declaration{Status: "partial", Cases: []string{"a", "b"}}, Regression},
		{"partial gained case", false, obs(Partial, "a", "b"), Declaration{Status: "partial", Cases: []string{"a"}}, Stale},
		{"invalid declaration", false, obs(Fail), Declaration{Status: "bogus"}, Invalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Decide(tc.security, tc.obs, tc.decl); got.Kind != tc.want {
				t.Fatalf("got %s (%s), want %s", got.Kind, got.Message, tc.want)
			}
		})
	}
}

func TestStatistics(t *testing.T) {
	cases := []struct {
		name    string
		pcts    []int
		wantN   int
		got, n  int
		pct     int
		wantErr bool
	}{
		{name: "50/50 size", pcts: []int{50, 50}, wantN: 598, got: 300, n: 598, pct: 50},
		{name: "80/20 size", pcts: []int{80, 20}, wantN: 383, got: 306, n: 383, pct: 80},
		{name: "60/40 size", pcts: []int{60, 40}, wantN: 574, got: 230, n: 574, pct: 40},
		{name: "deterministic share needs one sample", pcts: []int{100, 0}, wantN: 1, got: 1, n: 1, pct: 100},
		{name: "too small", pcts: []int{50}, wantN: 598, got: 20, n: 40, pct: 50, wantErr: true},
		{name: "outside tolerance", pcts: []int{50}, wantN: 598, got: 400, n: 598, pct: 50, wantErr: true},
		{name: "100% share must be exact", pcts: []int{100}, wantN: 1, got: 99, n: 100, pct: 100, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if n := SampleSize(tc.pcts...); n != tc.wantN {
				t.Errorf("SampleSize(%v) = %d, want %d", tc.pcts, n, tc.wantN)
			}
			if tol := Tolerance(tc.wantN, tc.pcts[0]); tol > Resolution+1e-9 || math.IsNaN(tol) {
				t.Errorf("Tolerance at SampleSize = %.2f, want <= %.0f", tol, Resolution)
			}
			if err := CheckShare("v", tc.got, tc.n, tc.pct); (err != nil) != tc.wantErr {
				t.Errorf("CheckShare: err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
