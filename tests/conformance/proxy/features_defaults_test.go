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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	echoClient "istio.io/istio/pkg/test/echo"
	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
)

// Data-plane defaults Istio configures without any user config (coverage/data/defaults/defaults-inventory.yaml),
// observed black-box on every role whose proxy has them. Most have nothing to strip, so their negative
// control is not applicable; the ones that compare against explicit config are two-sided.
func init() {
	Register(
		Feature{
			ID: "dataplane.no-default-route-timeout",
			Description: "Routes have no timeout by default: a response slower than Envoy's built-in 15s route timeout still succeeds, " +
				"while the same request through a route with an explicit 15s timeout fails with 504.",
			Roles:      l7Roles,
			RequiresL7: true,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - match: [{uri: {prefix: /ready}}]
    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
  - match: [{uri: {prefix: /explicit}}]
    timeout: 15s
    route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
  - route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
`))
				tg.Expect(t, Request{Path: "/ready"}, Version("v2"))
				slow := func(o *echo.CallOptions) { o.Timeout = 40 * time.Second }
				timed(t, tg, Request{Path: "/explicit?delay=16s", Opts: slow}, check.Status(504), 15*time.Second, 16*time.Second)
				timed(t, tg, Request{Path: "/default?delay=16s", Opts: slow}, check.OK(), 16*time.Second, 30*time.Second)
			},
		},
		Feature{
			ID: "dataplane.grpc-timeout-honored",
			Description: "A gRPC request's grpc-timeout header bounds the request at the proxy even though the route has no timeout; " +
				"Envoy reports DEADLINE_EXCEEDED (4) or UNAVAILABLE (14) depending on its runtime guard, and without the header the slow request succeeds.",
			Roles:      l7Roles,
			RequiresL7: true,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				grpcLike := func(timeout string) Request {
					h := H("content-type", "application/grpc", "te", "trailers")
					if timeout != "" {
						h.Set("grpc-timeout", timeout)
					}
					return Request{Path: "/?delay=3s", Method: "POST", HTTP2: true, Headers: h}
				}
				timed(t, tg, grpcLike(""), check.OK(), 3*time.Second, 10*time.Second)
				timed(t, tg, grpcLike("1S"), deadlineExceeded(), time.Second, 2500*time.Millisecond)
			},
		},
		Feature{
			ID:          "dataplane.expect-continue-proxied",
			Description: "An Expect: 100-continue request header is forwarded to the backend (the backend, not the proxy, decides whether to continue).",
			Roles:       l7Roles,
			RequiresL7:  true,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				tg.Expect(t, Request{Headers: H("Expect", "100-continue")}, check.RequestHeader("Expect", "100-continue"))
			},
		},
		Feature{
			ID: "dataplane.inbound-source-address",
			Description: "A sidecar forwards inbound requests to the application from 127.0.0.6, " +
				"so the application can tell proxied traffic from its own outbound traffic.",
			Roles:      []Role{RoleSidecar},
			RequiresL7: true,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				tg.Expect(t, Request{}, check.And(check.OK(), check.Each(func(r echoClient.Response) error {
					if ip, _ := serverPeer(r.RawContent); ip != "127.0.0.6" {
						return fmt.Errorf("expected the application to see peer 127.0.0.6, got %q", ip)
					}
					return nil
				})))
			},
		},
		Feature{
			ID:          "dataplane.permissive-mtls-default",
			Description: "Without PeerAuthentication, meshed workloads accept both mTLS from meshed clients and plaintext from clients outside the mesh (PERMISSIVE).",
			Roles:       []Role{RoleNode, RoleSidecar},
			Run: func(t framework.TestContext, s *Scenario) {
				s.Workload(t).Expect(t, Request{}, check.OK())
				plaintextCall(t, s, check.OK())
			},
		},
		Feature{
			ID:          "dataplane.outbound-allow-any",
			Description: "With the default outboundTrafficPolicy ALLOW_ANY, a meshed client reaches an address:port no service declares (passthrough).",
			Roles:       []Role{RoleSidecar},
			Covers:      []string{"MeshConfig.outboundTrafficPolicy.mode=ALLOW_ANY"},
			Run: func(t framework.TestContext, s *Scenario) {
				w := s.Workload(t)
				// The external echo's pod address on its container port: no Service port matches it.
				ip := s.Env.External[0].WorkloadsOrFail(t)[0].Address()
				w.Expect(t, Request{Address: ip, Port: 18080, Host: ip}, FromService(ExternalSvc))
			},
		},
		Feature{
			ID: "dataplane.inbound-passthrough",
			Description: "With the default inboundTrafficPolicy PASSTHROUGH, a meshed workload's proxy forwards traffic for a port " +
				"no Service declares to the application.",
			Roles:  []Role{RoleSidecar},
			Covers: []string{"MeshConfig.inboundTrafficPolicy.mode=PASSTHROUGH"},
			Run: func(t framework.TestContext, s *Scenario) {
				w := s.Workload(t)
				// The backend's app also listens on 18083, which its Service does not declare.
				ip := s.Backend(t).Instances[0].WorkloadsOrFail(t)[0].Address()
				w.Expect(t, Request{Address: ip, Port: 18083, Host: ip}, servedOnPort("18083"))
			},
		},
		Feature{
			ID: "dataplane.no-h2-upgrade",
			Description: "With the default h2UpgradePolicy DO_NOT_UPGRADE, the gateway forwards HTTP/1.1 requests to an " +
				"HTTP/1.1-declared backend as HTTP/1.1.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"MeshConfig.h2UpgradePolicy=DO_NOT_UPGRADE"},
			Run: func(t framework.TestContext, s *Scenario) {
				// Out-of-mesh backend, as in meshconfig.h2-upgrade-policy: a backend sidecar would
				// re-encode the request toward the app.
				tg := s.Target(t, TargetOptions{Routing: RouteNone})
				s.Apply(t, externalRoute(tg.Gateway()))
				tg.Expect(t, Request{}, check.And(check.OK(), check.Protocol("HTTP/1.1")))
			},
		},
		Feature{
			ID:          "dataplane.access-log-text",
			Description: "With the default accessLogEncoding TEXT, the gateway writes access log lines in Istio's default text format.",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers:      []string{"MeshConfig.accessLogEncoding=TEXT"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				expectAccessLogLine(t, tg, func(marker, line string) error {
					var entry map[string]any
					if json.Unmarshal([]byte(line), &entry) == nil {
						return fmt.Errorf("access log line is JSON, expected text: %s", line)
					}
					// Istio's default format: [%START_TIME%] "%REQ(:METHOD)% %REQ(X-ENVOY-ORIGINAL-PATH?:PATH)% %PROTOCOL%" ...
					if !strings.HasPrefix(line, "[") || !strings.Contains(line, `"GET `+marker+` HTTP/1.1" 200`) {
						return fmt.Errorf("access log line is not in the default text format: %s", line)
					}
					return nil
				})
			},
		},
		Feature{
			ID:          "dataplane.xff.mesh-preserved",
			Description: "A mesh proxy forwards a client-supplied X-Forwarded-For unchanged and adds none of its own (only edge proxies use the remote address).",
			Roles:       []Role{RoleSidecar, RoleWaypoint},
			RequiresL7:  true,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				tg.Expect(t, Request{Headers: H("X-Forwarded-For", "1.2.3.4")}, check.RequestHeader("X-Forwarded-For", "1.2.3.4"))
				tg.Expect(t, Request{}, RequestHeaderAbsent("X-Forwarded-For"))
			},
		},
		Feature{
			ID: "dataplane.xfcc.mesh-client-identity",
			Description: "The backend's proxy passes the client's mesh identity to the application in X-Forwarded-Client-Cert (URI=spiffe://...), " +
				"replacing a client-supplied value.",
			Roles:      []Role{RoleSidecar},
			RequiresL7: true,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				tg.Expect(t, Request{Headers: H("X-Forwarded-Client-Cert", "By=spiffe://evil/ns/x/sa/y;URI=spiffe://evil/ns/x/sa/y")}, check.And(
					RequestHeaderContains("X-Forwarded-Client-Cert", "URI=spiffe://"+s.ClientPrincipal()),
					headerNotContains("X-Forwarded-Client-Cert", "spiffe://evil")))
			},
		},
		Feature{
			ID:          "dataplane.request-id.mesh-preserved",
			Description: "A mesh proxy generates X-Request-Id when the client sends none, and preserves a client-supplied one (only edge proxies replace it).",
			Roles:       []Role{RoleSidecar, RoleWaypoint},
			RequiresL7:  true,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				tg.Expect(t, Request{}, RequestHeaderPresent("X-Request-Id"))
				tg.Expect(t, Request{Headers: H("X-Request-Id", "pcs-fixed-id")}, check.RequestHeader("X-Request-Id", "pcs-fixed-id"))
			},
		},
		Feature{
			ID:          "dataplane.trace-context-propagation.mesh",
			Description: "W3C trace context from the client is propagated through the mesh proxy to the backend (same trace id).",
			Roles:       []Role{RoleSidecar, RoleWaypoint},
			RequiresL7:  true,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
				tg.Expect(t, Request{Headers: H("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")}, RequestHeaderContains("traceparent", traceID))
			},
		},
		Feature{
			ID:          "dataplane.path-normalization.mesh",
			Description: "A mesh proxy normalizes paths (dot segments resolved) before route matching, so /x/../admin matches a /admin route.",
			Roles:       []Role{RoleSidecar, RoleWaypoint},
			RequiresL7:  true,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone})
				s.Apply(t, tg.HTTPRoute(`
  - matches:
    - path: {type: Exact, value: /admin}
    backendRefs:
    - {name: {{.DestV2}}, port: 80}
  - backendRefs:
    - {name: {{.DestV1}}, port: 80}
`))
				tg.Expect(t, Request{Path: "/admin"}, Version("v2"))
				tg.Expect(t, Request{Path: "/x/../admin"}, Version("v2"))
			},
		},
		Feature{
			ID:          "protocol.websocket.mesh",
			Description: "WebSocket upgrades are proxied through a mesh proxy to the backend.",
			Roles:       []Role{RoleSidecar, RoleWaypoint},
			RequiresL7:  true,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				tg.Expect(t, Request{Scheme: scheme.WebSocket}, check.OK())
			},
		},
		Feature{
			ID: "dataplane.default-retry-on-connect-failure.mesh",
			Description: "Without explicit retry config, a mesh proxy retries requests on connection failure (Istio default retry policy), " +
				"so a single unavailable endpoint does not cause errors.",
			Roles:      []Role{RoleSidecar, RoleWaypoint},
			RequiresL7: true,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone})
				// One live endpoint and one dead one (a closed port on another pod; endpoints sharing
				// an IP would be deduplicated).
				tg.ServiceEntry(t, `
  location: MESH_EXTERNAL
  resolution: STATIC
  ports:
  - {number: 80, name: http, protocol: HTTP}
  endpoints:
  - {address: {{.IP}}, ports: {http: 18080}}
  - {address: {{.DeadIP}}, ports: {http: 1}}
`, map[string]any{"IP": s.Env.External[0].WorkloadsOrFail(t)[0].Address(), "DeadIP": clientIP(t, s)})
				tg.Expect(t, Request{Count: 30, Opts: newConnPerRequest}, MaxFailures(1))
			},
		},
	)
}

// timed sends one request, retrying until c passes, and requires the successful attempt's duration
// to be within [lower, upper): a two-sided timing check.
func timed(t framework.TestContext, tg *Target, r Request, c echo.Checker, lower, upper time.Duration) {
	t.Helper()
	var last error
	for attempt := 0; attempt < 5; attempt++ {
		start := time.Now()
		res, err := tg.Call(t, r)
		d := time.Since(start)
		if cerr := c(res, err); cerr != nil {
			last = fmt.Errorf("attempt %d: %v (call error: %v)", attempt, cerr, err)
			continue
		}
		if d < lower || d >= upper {
			last = fmt.Errorf("attempt %d: took %v, expected [%v, %v)", attempt, d, lower, upper)
			continue
		}
		return
	}
	t.Fatalf("%s: %v", r.Path, last)
}

// deadlineExceeded checks that a gRPC request hit the deadline imposed by its grpc-timeout header.
// Envoy reports DEADLINE_EXCEEDED (grpc-status 4) once the 1.38
// grpc_timeout_returns_deadline_exceeded runtime guard is enabled, and UNAVAILABLE (14) otherwise;
// an HTTP 504 is also accepted.
func deadlineExceeded() echo.Checker {
	return func(result echo.CallResult, err error) error {
		if len(result.Responses) == 0 {
			return fmt.Errorf("no response: %v", err)
		}
		for _, r := range result.Responses {
			gs := r.ResponseHeaders.Get("grpc-status")
			if r.Code != "504" && gs != "4" && gs != "14" {
				return fmt.Errorf("expected a deadline timeout (grpc-status 4 or 14, or 504), got code %s, headers %v", r.Code, r.ResponseHeaders)
			}
		}
		return nil
	}
}
