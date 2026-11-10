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
	"io"
	"net/http"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/rand"

	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo/check"
	"istio.io/istio/pkg/test/util/retry"
)

// Telemetry: access logs, standard metrics, and the Telemetry API. Observed via gateway pod logs and
// the pod's merged Prometheus endpoint (:15020/stats/prometheus), both of which Istio defines for
// every gateway implementation.
func init() {
	Register(
		Feature{
			ID:          "telemetry.accesslog.mesh-default",
			Description: "With meshConfig.accessLogFile set, the proxy writes an access log line per request.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				marker := "/pcs-log-" + rand.String(10)
				tg.Expect(t, Request{Path: marker}, check.OK())
				expectGatewayLogs(t, tg, marker, true)
			},
		},
		Feature{
			ID:          "telemetry.accesslog.disabled",
			Description: "A Telemetry resource attached to the proxy with accessLogging.disabled stops access logs.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"Telemetry.targetRefs", "Telemetry.accessLogging", "Telemetry.accessLogging.disabled"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				nonce := rand.String(8)
				s.Apply(t, tg.Telemetry(`
  accessLogging:
  - disabled: true
`+WitnessMetrics(nonce)))
				// Access logging has no client-visible effect; the witness in the same object proves
				// the config is in effect before absence is checked.
				tg.WitnessTelemetry(t, nonce)
				marker := "/pcs-nolog-" + rand.String(10)
				tg.Expect(t, Request{Path: marker, Count: 3}, check.OK())
				expectNoGatewayLog(t, tg, marker, "")
			},
		},
		Feature{
			ID:          "telemetry.accesslog.filter",
			Description: "A Telemetry accessLogging CEL filter logs only matching requests.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"Telemetry.accessLogging.providers", "Telemetry.accessLogging.filter", "Telemetry.accessLogging.filter.expression"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				nonce := rand.String(8)
				s.Apply(t, tg.Telemetry(`
  accessLogging:
  - providers: [{name: envoy}]
    filter:
      expression: response.code >= 400
`+WitnessMetrics(nonce)))
				tg.WitnessTelemetry(t, nonce)
				// bad is sent after ok: once bad is logged, ok would have been logged too.
				ok, bad := "/pcs-ok-"+rand.String(10), "/pcs-bad-"+rand.String(10)
				tg.Expect(t, Request{Path: ok}, check.OK())
				tg.Expect(t, Request{Path: bad + "?codes=418"}, check.Status(418))
				expectNoGatewayLog(t, tg, ok, bad)
			},
		},
		Feature{
			ID:          "telemetry.accesslog.disabled-named-provider",
			Description: "A Telemetry accessLogging entry naming the provider with disabled: true stops access logs.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"Telemetry.accessLogging.providers.name"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				nonce := rand.String(8)
				s.Apply(t, tg.Telemetry(`
  accessLogging:
  - providers: [{name: envoy}]
    disabled: true
`+WitnessMetrics(nonce)))
				tg.WitnessTelemetry(t, nonce)
				marker := "/pcs-nolog-" + rand.String(10)
				tg.Expect(t, Request{Path: marker, Count: 3}, check.OK())
				expectNoGatewayLog(t, tg, marker, "")
			},
		},
		Feature{
			ID:          "telemetry.accesslog.selector",
			Description: "A Telemetry resource attached by selector (labels of the proxy's pods) disables access logs.",
			Roles:       []Role{RoleIngress, RoleSidecar},
			RequiresL7:  true,
			Covers:      []string{"Telemetry.selector", "Telemetry.selector.matchLabels"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				nonce := rand.String(8)
				s.Apply(t, `
apiVersion: telemetry.istio.io/v1
kind: Telemetry
metadata:
  name: {{.Name}}
spec:
  selector:
    matchLabels:
      {{.WorkloadSelector}}
  accessLogging:
  - disabled: true
{{.Witness}}`, map[string]any{"Witness": WitnessMetrics(nonce)})
				tg.WitnessTelemetry(t, nonce)
				marker := "/pcs-nolog-" + rand.String(10)
				tg.Expect(t, Request{Path: marker, Count: 3}, check.OK())
				expectNoGatewayLog(t, tg, marker, "")
			},
		},
		Feature{
			ID:          "telemetry.metrics.istio-requests-total",
			Description: "The proxy exposes the standard istio_requests_total metric with destination and response code labels on :15020/stats/prometheus.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				tg.Expect(t, Request{Count: 5}, check.OK())
				dest := `destination_workload="` + tg.DestinationWorkload() + `"`
				retry.UntilSuccessOrFail(t, func() error {
					m, err := tg.Metrics(t)
					if err != nil {
						return err
					}
					if len(metricLines(m, "istio_requests_total", dest, `response_code="200"`)) == 0 {
						return fmt.Errorf("no istio_requests_total series with %s and response_code=200", dest)
					}
					return nil
				}, retry.Timeout(time.Minute))
			},
		},
		Feature{
			ID:          "telemetry.metrics.tcp",
			Description: "The gateway exposes standard istio_tcp_connections_opened_total for TCP routes.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{Listeners: `
  - {name: tcp, port: 9090, protocol: TCP}
`})
				s.Apply(t, `
apiVersion: gateway.networking.k8s.io/v1alpha2
kind: TCPRoute
metadata:
  name: {{.Name}}
spec:
  parentRefs: [{name: {{.GW}}}]
  rules:
  - backendRefs: [{name: {{.BackendV1}}, port: 9090}]
`, map[string]any{"GW": gw.Name})
				gw.Expect(t, Request{Port: 9090, Scheme: "tcp", Count: 3, Opts: newConnPerRequest}, check.OK())
				retry.UntilSuccessOrFail(t, func() error {
					m, err := gw.Metrics(t)
					if err != nil {
						return err
					}
					if !strings.Contains(m, "istio_tcp_connections_opened_total{") {
						return fmt.Errorf("no istio_tcp_connections_opened_total series")
					}
					return nil
				}, retry.Timeout(time.Minute))
			},
		},
		Feature{
			ID:          "telemetry.metrics.disabled",
			Description: "A Telemetry metrics override with disabled: true stops istio_requests_total from counting the proxy's requests.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"Telemetry.metrics.overrides.disabled", "Telemetry.metrics.overrides.match.metric", "Telemetry.metrics.overrides.match.metric=REQUEST_COUNT",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				if s.Proxy.Role == RoleSidecar {
					// The inbound sidecar applies the new stats config only to new connections, and the
					// client sidecar would keep reusing the one the baseline opened.
					s.NoConnectionReuse(t, tg.Host())
				}
				dest := `destination_workload="` + tg.DestinationWorkload() + `"`
				// Baseline: the metric must exist before it can be disabled.
				retry.UntilSuccessOrFail(t, func() error {
					if _, err := tg.Call(t, Request{}); err != nil {
						return err
					}
					m, err := tg.Metrics(t)
					if err != nil {
						return err
					}
					if len(metricLines(m, "istio_requests_total", dest)) == 0 {
						return fmt.Errorf("baseline: no istio_requests_total with %s before disabling", dest)
					}
					return nil
				}, retry.Timeout(time.Minute), retry.Delay(2*time.Second))
				nonce := rand.String(8)
				s.Apply(t, tg.Telemetry(`
  metrics:
  - providers: [{name: prometheus}]
    overrides:
    - match: {metric: REQUEST_COUNT}
      disabled: true
`+WitnessMetricsOverride(nonce)))
				tg.WitnessTelemetry(t, nonce)
				// Existing counters may keep being exported; disabled means they stop increasing. The
				// witness histogram counts the same requests and is updated after counters, so once it
				// includes the batch, istio_requests_total would have too.
				m, err := tg.Metrics(t)
				if err != nil {
					t.Fatal(err)
				}
				before, witnessBefore := metricSum(m, "istio_requests_total", dest), witnessCount(m, nonce)
				tg.Expect(t, Request{Count: 5}, check.OK())
				retry.UntilSuccessOrFail(t, func() error {
					if m, err = tg.Metrics(t); err != nil {
						return err
					}
					if got := witnessCount(m, nonce); got < witnessBefore+5 {
						return fmt.Errorf("witness histogram has %v of %v requests", got-witnessBefore, 5)
					}
					return nil
				}, retry.Timeout(time.Minute), retry.Delay(time.Second))
				if after := metricSum(m, "istio_requests_total", dest); after != before {
					t.Fatalf("istio_requests_total kept increasing after disabling REQUEST_COUNT (%v -> %v)", before, after)
				}
			},
		},
		Feature{
			ID:          "telemetry.metrics.tag-remove",
			Description: "A Telemetry tagOverrides REMOVE drops a standard label from istio_requests_total.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"Telemetry.metrics.overrides.tagOverrides.operation", "Telemetry.metrics.overrides.tagOverrides.operation=REMOVE"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				dest := `destination_workload="` + tg.DestinationWorkload() + `"`
				nonce := rand.String(8)
				s.Apply(t, tg.Telemetry(`
  metrics:
  - providers: [{name: prometheus}]
    overrides:
    - match: {metric: REQUEST_COUNT}
      tagOverrides:
        request_protocol:
          operation: REMOVE
`+WitnessMetricsOverride(nonce)))
				tg.WitnessTelemetry(t, nonce)
				// Series exported before the override keep the label; requests after the witness must
				// land in a series without it.
				retry.UntilSuccessOrFail(t, func() error {
					if _, err := tg.Call(t, Request{}); err != nil {
						return err
					}
					m, err := tg.Metrics(t)
					if err != nil {
						return err
					}
					for _, l := range metricLines(m, "istio_requests_total", dest) {
						if !strings.Contains(l, "request_protocol=") {
							return nil
						}
					}
					return fmt.Errorf("no istio_requests_total series with %s without request_protocol", dest)
				}, retry.Timeout(2*time.Minute), retry.Delay(2*time.Second))
			},
		},
		// Metrics overrides, one feature per standard metric: a tagOverrides value on the metric adds
		// the label to that metric's series (see metricTagOverride).
		Feature{
			ID:          "telemetry.metrics.tag-override",
			Description: "A Telemetry metrics override (tagOverrides) on REQUEST_COUNT adds a custom label to istio_requests_total.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"Telemetry.metrics", "Telemetry.metrics.overrides", "Telemetry.metrics.overrides.tagOverrides", "Telemetry.metrics.overrides.match",
				"Telemetry.metrics.providers", "Telemetry.metrics.providers.name", "Telemetry.metrics.overrides.tagOverrides.value",
			},
			Run: metricTagOverride("REQUEST_COUNT", "istio_requests_total", ""),
		},
		Feature{
			ID:          "telemetry.metrics.tag-override.request-duration",
			Description: "A Telemetry tagOverrides value on REQUEST_DURATION adds the label to istio_request_duration_milliseconds.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"Telemetry.metrics.overrides.match.metric=REQUEST_DURATION"},
			Run:         metricTagOverride("REQUEST_DURATION", "istio_request_duration_milliseconds_count", ""),
		},
		Feature{
			ID:          "telemetry.metrics.tag-override.request-size",
			Description: "A Telemetry tagOverrides value on REQUEST_SIZE adds the label to istio_request_bytes.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"Telemetry.metrics.overrides.match.metric=REQUEST_SIZE"},
			Run:         metricTagOverride("REQUEST_SIZE", "istio_request_bytes_count", ""),
		},
		Feature{
			ID:          "telemetry.metrics.tag-override.response-size",
			Description: "A Telemetry tagOverrides value on RESPONSE_SIZE adds the label to istio_response_bytes.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"Telemetry.metrics.overrides.match.metric=RESPONSE_SIZE"},
			Run:         metricTagOverride("RESPONSE_SIZE", "istio_response_bytes_count", ""),
		},
		Feature{
			ID: "telemetry.metrics.tag-override.all-metrics-upsert",
			Description: "A Telemetry tagOverrides UPSERT on ALL_METRICS adds the label to every standard HTTP metric " +
				"(checked on istio_requests_total and istio_response_bytes).",
			Roles:      l7Roles,
			RequiresL7: true,
			Covers:     []string{"Telemetry.metrics.overrides.match.metric=ALL_METRICS", "Telemetry.metrics.overrides.tagOverrides.operation=UPSERT"},
			Run:        metricTagOverride("ALL_METRICS", "istio_requests_total", "istio_response_bytes_count"),
		},
		// Tracing: sampling decisions are propagated to the backend in the B3 headers the proxy
		// injects (x-b3-sampled), so they are observable without a tracing backend. The provider is a
		// MeshConfig extension provider pointing at istiod (span reports are simply refused there).
		Feature{
			ID: "telemetry.tracing.sampling-all",
			Description: "A Telemetry tracing randomSamplingPercentage of 100 with a Zipkin provider makes the proxy sample every request " +
				"(x-b3-sampled: 1 at the backend).",
			Roles:      l7Roles,
			RequiresL7: true,
			Covers: []string{
				"Telemetry.tracing", "Telemetry.tracing.providers", "Telemetry.tracing.providers.name", "Telemetry.tracing.randomSamplingPercentage",
				"MeshConfig.extensionProviders.zipkin", "MeshConfig.extensionProviders.zipkin.service", "MeshConfig.extensionProviders.zipkin.port",
			},
			MeshConfig: tracingProvider,
			Run:        tracingSampled("100", "1"),
		},
		Feature{
			ID:          "telemetry.tracing.sampling-none",
			Description: "A Telemetry tracing randomSamplingPercentage of 0 makes the proxy start traces without sampling them (x-b3-sampled: 0 at the backend).",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"Telemetry.tracing.match", "Telemetry.tracing.match.mode", "Telemetry.tracing.match.mode=CLIENT_AND_SERVER"},
			MeshConfig:  tracingProvider,
			Run:         tracingSampled("0", "0"),
		},
	)
}

// tracingProvider is the MeshConfig patch of the tracing family.
const tracingProvider = `
extensionProviders:
- name: pcs-zipkin
  zipkin:
    service: istiod.{{.SystemNamespace}}.svc.cluster.local
    port: 15014
`

// tracingSampled is the body of the tracing family: a Telemetry tracing entry for the pcs-zipkin
// provider with the sampling percentage (and match mode CLIENT_AND_SERVER) must make the backend see
// x-b3-sampled: want on requests that carry no trace context.
func tracingSampled(pct, want string) func(t framework.TestContext, s *Scenario) {
	return func(t framework.TestContext, s *Scenario) {
		tg := s.Target(t, TargetOptions{})
		s.Apply(t, tg.Telemetry(`
  tracing:
  - providers: [{name: pcs-zipkin}]
    randomSamplingPercentage: `+pct+`
    match: {mode: CLIENT_AND_SERVER}
`))
		tg.Expect(t, Request{Count: 5}, check.And(check.OK(), check.RequestHeader("X-B3-Sampled", want)))
	}
}

// metricTagOverride is the body of the tagOverrides family: a Telemetry override on metric adds
// pcs_tag=<random> (operation UPSERT, the default when only value is set), and the series of each
// named Prometheus metric for the feature's destination must carry it.
func metricTagOverride(metric string, series ...string) func(t framework.TestContext, s *Scenario) {
	return func(t framework.TestContext, s *Scenario) {
		tg := s.Target(t, TargetOptions{})
		dest := `destination_workload="` + tg.DestinationWorkload() + `"`
		value := "pcs-" + rand.String(8)
		op := ""
		if metric == "ALL_METRICS" {
			op = "\n          operation: UPSERT"
		}
		s.Apply(t, tg.Telemetry(`
  metrics:
  - providers: [{name: prometheus}]
    overrides:
    - match: {metric: `+metric+`}
      tagOverrides:
        pcs_tag:`+op+`
          value: "'`+value+`'"
`))
		label := `pcs_tag="` + value + `"`
		retry.UntilSuccessOrFail(t, func() error {
			if _, err := tg.Call(t, Request{}); err != nil {
				return err
			}
			m, err := tg.Metrics(t)
			if err != nil {
				return err
			}
			for _, name := range series {
				if name != "" && len(metricLines(m, name, dest, label)) == 0 {
					return fmt.Errorf("no %s series with %s and %s", name, dest, label)
				}
			}
			return nil
		}, retry.Timeout(2*time.Minute), retry.Delay(2*time.Second))
	}
}

// metricLines returns the sample lines of the metric (exact name) whose labels contain every filter.
func metricLines(m, name string, filters ...string) []string {
	var out []string
	for _, l := range strings.Split(m, "\n") {
		if !strings.HasPrefix(l, name+"{") {
			continue
		}
		ok := true
		for _, f := range filters {
			if !strings.Contains(l, f) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, l)
		}
	}
	return out
}

// metricSum sums the samples of metricLines.
func metricSum(m, name string, filters ...string) float64 {
	var sum float64
	for _, l := range metricLines(m, name, filters...) {
		f := strings.Fields(l)
		var v float64
		if _, err := fmt.Sscanf(f[len(f)-1], "%g", &v); err == nil {
			sum += v
		}
	}
	return sum
}

// expectGatewayLogs waits until the gateway logs contain the marker. For absence, use
// expectNoGatewayLog (witness_test.go).
func expectGatewayLogs(t framework.TestContext, gw telemetrySource, marker string, want bool) {
	t.Helper()
	if !want {
		t.Fatalf("expectGatewayLogs(want=false) is racy; use expectNoGatewayLog after a witness")
	}
	retry.UntilSuccessOrFail(t, func() error {
		logs, err := gw.Logs(t)
		if err != nil {
			return err
		}
		if !strings.Contains(logs, marker) {
			return fmt.Errorf("proxy did not log %q", marker)
		}
		return nil
	}, retry.Timeout(time.Minute), retry.Delay(2*time.Second))
}

// Metrics scrapes the merged Prometheus endpoint (:15020/stats/prometheus) of the first gateway pod.
func (g *Gateway) Metrics(t framework.TestContext) (string, error) {
	pods := g.Pods(t)
	if len(pods) == 0 {
		return "", fmt.Errorf("no gateway pods")
	}
	fw, err := t.Clusters().Default().NewPortForwarder(pods[0].Name, g.Namespace, "", 0, 15020)
	if err != nil {
		return "", err
	}
	if err := fw.Start(); err != nil {
		return "", err
	}
	defer fw.Close()
	resp, err := http.Get("http://" + fw.Address() + "/stats/prometheus")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}
