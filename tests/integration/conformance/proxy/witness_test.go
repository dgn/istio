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
	"fmt"
	"strconv"
	"strings"
	"time"

	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/util/retry"
)

// Propagation witnesses.
//
// Config reaches the proxy asynchronously, and black-box tests cannot read config dumps. A positive
// check ("returns 403") is self-synchronizing: Expect retries until it holds. A negative check ("no
// access log line") is not: it holds trivially before the config arrives, and sleeping "long enough"
// is both slow and racy. Negative checks must therefore come after a *witness*: a positive,
// observable effect of the same config object, which the proxy receives in the same push. Features
// must not call time.Sleep (enforced by the coverage check); the bounded waits below are the only
// places where time is used, each justified by a documented latency bound.
//
// When the witness never appears, the run is Indeterminate: propagation could not be established. If
// the proxy ignores the whole object, the negative control on the same proxy fails at the same place,
// and the outcome becomes Ignored, which is the correct finding.

// witnessTimeout bounds how long a witness may take to appear.
const witnessTimeout = 2 * time.Minute

// logFlushBound is the longest an access log line may take to reach the pod log after the request
// completed: Envoy flushes access log files every 10s (--file-flush-interval-msec default), plus
// margin for the kubelet. Absence of a log line can only be concluded after this bound, and only for
// requests sent after a witness.
const logFlushBound = 15 * time.Second

// WitnessMetricsOverride returns a Telemetry metrics override (an item of spec.metrics[].overrides)
// that adds the label pcs_witness=<nonce> to istio_request_duration_milliseconds. Put it in the same
// Telemetry object as the config under test, and call WitnessTelemetry before negative checks. It
// relies on tagOverrides (feature telemetry.metrics.tag-override); a proxy that lacks it is
// Indeterminate for features using this witness.
func WitnessMetricsOverride(nonce string) string {
	return `    - match: {metric: REQUEST_DURATION}
      tagOverrides:
        pcs_witness:
          value: "'` + nonce + `'"
`
}

// WitnessMetrics returns a complete spec.metrics section holding only the witness override, for
// Telemetry objects that do not configure metrics themselves.
func WitnessMetrics(nonce string) string {
	return `  metrics:
  - providers: [{name: prometheus}]
    overrides:
` + WitnessMetricsOverride(nonce)
}

// WitnessTelemetry sends requests until the witness label appears in the gateway's metrics, proving
// that the Telemetry object carrying WitnessMetricsOverride(nonce) is in effect.
func (g *Gateway) WitnessTelemetry(t framework.TestContext, nonce string) {
	t.Helper()
	label := `pcs_witness="` + nonce + `"`
	err := retry.UntilSuccess(func() error {
		if _, err := g.Call(t, Request{}); err != nil {
			return err
		}
		m, err := g.Metrics(t)
		if err != nil {
			return err
		}
		if !strings.Contains(m, label) {
			return fmt.Errorf("witness %s not in metrics yet", label)
		}
		return nil
	}, retry.Timeout(witnessTimeout), retry.Delay(2*time.Second))
	if err != nil {
		g.s.Indeterminate(t, "Telemetry propagation witness never appeared (metrics tagOverrides): "+err.Error())
	}
}

// witnessCount returns the number of requests recorded under the witness label (the histogram's
// _count). Envoy updates histograms only on stats flush, after counters, so once the witness count
// includes a batch of requests, every counter already reflects them.
func witnessCount(m, nonce string) float64 {
	label := `pcs_witness="` + nonce + `"`
	var sum float64
	for _, l := range strings.Split(m, "\n") {
		if strings.HasPrefix(l, "istio_request_duration_milliseconds_count{") && strings.Contains(l, label) {
			f := strings.Fields(l)
			if v, err := strconv.ParseFloat(f[len(f)-1], 64); err == nil {
				sum += v
			}
		}
	}
	return sum
}

// expectNoGatewayLog asserts that marker is never logged. Requests carrying the marker must have been
// sent after a witness. With flushWitness, a path requested *after* the marker that is expected in
// the log, the check is exact: logs are ordered, so once flushWitness is logged, marker would have
// been too. Without it, absence is concluded after logFlushBound.
func expectNoGatewayLog(t framework.TestContext, gw *Gateway, marker, flushWitness string) {
	t.Helper()
	if flushWitness != "" {
		expectGatewayLogs(t, gw, flushWitness, true)
	} else {
		time.Sleep(logFlushBound)
	}
	logs, err := gw.Logs(t)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs, marker) {
		t.Fatalf("gateway logged %q, expected no log", marker)
	}
}
