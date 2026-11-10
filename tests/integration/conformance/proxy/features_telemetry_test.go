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
			Description: "With meshConfig.accessLogFile set, the gateway writes an access log line per request.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				s.Apply(t, httpRouteTo(gw, "{{.BackendV1}}", 80))
				marker := "/pcs-log-" + rand.String(10)
				gw.Expect(t, Request{Path: marker}, check.OK())
				expectGatewayLogs(t, gw, marker, true)
			},
		},
		Feature{
			ID:          "telemetry.accesslog.disabled",
			Description: "A Telemetry resource targeting the gateway with accessLogging.disabled stops access logs.",
			Roles:       ingressRole,
			Covers:      []string{"Telemetry.targetRefs", "Telemetry.accessLogging", "Telemetry.accessLogging.disabled"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				s.Apply(t, httpRouteTo(gw, "{{.BackendV1}}", 80))
				nonce := rand.String(8)
				s.Apply(t, telemetryFor(gw, `
  accessLogging:
  - disabled: true
`+WitnessMetrics(nonce)))
				// Access logging has no client-visible effect; the witness in the same object proves
				// the config is in effect before absence is checked.
				gw.WitnessTelemetry(t, nonce)
				marker := "/pcs-nolog-" + rand.String(10)
				gw.Expect(t, Request{Path: marker, Count: 3}, check.OK())
				expectNoGatewayLog(t, gw, marker, "")
			},
		},
		Feature{
			ID:          "telemetry.accesslog.filter",
			Description: "A Telemetry accessLogging CEL filter logs only matching requests.",
			Roles:       ingressRole,
			Covers:      []string{"Telemetry.accessLogging.providers", "Telemetry.accessLogging.filter", "Telemetry.accessLogging.filter.expression"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				s.Apply(t, httpRouteTo(gw, "{{.BackendV1}}", 80))
				nonce := rand.String(8)
				s.Apply(t, telemetryFor(gw, `
  accessLogging:
  - providers: [{name: envoy}]
    filter:
      expression: response.code >= 400
`+WitnessMetrics(nonce)))
				gw.WitnessTelemetry(t, nonce)
				// bad is sent after ok: once bad is logged, ok would have been logged too.
				ok, bad := "/pcs-ok-"+rand.String(10), "/pcs-bad-"+rand.String(10)
				gw.Expect(t, Request{Path: ok}, check.OK())
				gw.Expect(t, Request{Path: bad + "?codes=418"}, check.Status(418))
				expectNoGatewayLog(t, gw, ok, bad)
			},
		},
		Feature{
			ID:          "telemetry.metrics.istio-requests-total",
			Description: "The gateway exposes the standard istio_requests_total metric with destination and response code labels on :15020/stats/prometheus.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				s.Apply(t, httpRouteTo(gw, "{{.BackendV1}}", 80))
				gw.Expect(t, Request{Count: 5}, check.OK())
				retry.UntilSuccessOrFail(t, func() error {
					m, err := gw.Metrics(t)
					if err != nil {
						return err
					}
					for _, l := range strings.Split(m, "\n") {
						if strings.HasPrefix(l, "istio_requests_total{") &&
							strings.Contains(l, `response_code="200"`) &&
							strings.Contains(l, `destination_service_name="`+BackendSvc+`-v1"`) {
							return nil
						}
					}
					return fmt.Errorf("no istio_requests_total series for %s-v1 with response_code=200", BackendSvc)
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
			ID:          "telemetry.metrics.tag-override",
			Description: "A Telemetry metrics override (tagOverrides) adds a custom label to istio_requests_total.",
			Roles:       ingressRole,
			Covers:      []string{"Telemetry.metrics", "Telemetry.metrics.overrides", "Telemetry.metrics.overrides.tagOverrides", "Telemetry.metrics.overrides.match"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				s.Apply(t, httpRouteTo(gw, "{{.BackendV1}}", 80))
				s.Apply(t, telemetryFor(gw, `
  metrics:
  - providers: [{name: prometheus}]
    overrides:
    - match: {metric: REQUEST_COUNT}
      tagOverrides:
        pcs_tag:
          value: "'pcs-value'"
`))
				retry.UntilSuccessOrFail(t, func() error {
					if _, err := gw.Call(t, Request{}); err != nil {
						return err
					}
					m, err := gw.Metrics(t)
					if err != nil {
						return err
					}
					if !strings.Contains(m, `pcs_tag="pcs-value"`) {
						return fmt.Errorf("custom tag not found in istio_requests_total")
					}
					return nil
				}, retry.Timeout(2*time.Minute), retry.Delay(2*time.Second))
			},
		},
	)
}

func telemetryFor(gw *Gateway, body string) string {
	return `
apiVersion: telemetry.istio.io/v1
kind: Telemetry
metadata:
  name: {{.Name}}
spec:
  targetRefs:
  - {kind: Gateway, group: gateway.networking.k8s.io, name: ` + gw.Name + `}
` + body
}

// expectGatewayLogs waits until the gateway logs contain the marker. For absence, use
// expectNoGatewayLog (witness_test.go).
func expectGatewayLogs(t framework.TestContext, gw *Gateway, marker string, want bool) {
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
			return fmt.Errorf("gateway did not log %q", marker)
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

func init() {
	Register(Feature{
		ID:          "telemetry.accesslog.disabled-named-provider",
		Description: "A Telemetry accessLogging entry naming the provider with disabled: true stops access logs.",
		Roles:       ingressRole,
		Covers:      []string{"Telemetry.accessLogging.providers.name"},
		Run: func(t framework.TestContext, s *Scenario) {
			gw := s.NewGateway(t, GatewayOptions{})
			s.Apply(t, httpRouteTo(gw, "{{.BackendV1}}", 80))
			nonce := rand.String(8)
			s.Apply(t, telemetryFor(gw, `
  accessLogging:
  - providers: [{name: envoy}]
    disabled: true
`+WitnessMetrics(nonce)))
			gw.WitnessTelemetry(t, nonce)
			marker := "/pcs-nolog-" + rand.String(10)
			gw.Expect(t, Request{Path: marker, Count: 3}, check.OK())
			expectNoGatewayLog(t, gw, marker, "")
		},
	})
}
