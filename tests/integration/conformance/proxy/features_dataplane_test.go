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
	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
)

// Data-plane defaults and ProxyConfig-driven behaviors of the gateway: client address handling,
// forwarded headers, PROXY protocol, path handling, trace context.
func init() {
	Register(
		Feature{
			ID:          "dataplane.xff.append-client-ip",
			Description: "The gateway appends the client's address to X-Forwarded-For.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				s.Apply(t, httpRouteToShared(s, "{{.BackendV1}}", 80), s.sharedVars())
				gw := s.SharedGateway()
				gw.Expect(t, Request{Address: gw.PodIP(t)}, RequestHeaderContains("X-Forwarded-For", clientIP(t, s)))
			},
		},
		Feature{
			ID:          "dataplane.xff.untrusted-by-default",
			Description: "By default (numTrustedProxies=0), a client-supplied X-Forwarded-For does not change the client address used for authorization.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := policyGateway(t, s)
				s.Apply(t, authzPolicy(gw, "DENY", `
  - from:
    - source:
        remoteIpBlocks: ["1.2.3.4/32"]
  - from:
    - source:
        remoteIpBlocks: ["{{.ClientIP}}/32"]
    to:
    - operation:
        paths: ["/probe"]
`), map[string]any{"ClientIP": clientIP(t, s)})
				pod := gw.PodIP(t)
				// The policy is active and keyed on the real client address...
				gw.Expect(t, Request{Address: pod, Path: "/probe"}, check.Status(403))
				// ...and a spoofed X-Forwarded-For does not change that address.
				gw.ExpectStable(t, Request{Address: pod, Headers: H("X-Forwarded-For", "1.2.3.4")}, check.OK())
			},
		},
		Feature{
			ID:          "dataplane.xff.num-trusted-proxies",
			Description: "gatewayTopology.numTrustedProxies (proxy.istio.io/config on the Gateway) makes the gateway trust X-Forwarded-For hops for the client address.",
			Roles:       ingressRole,
			Covers:      []string{"annotation:proxy.istio.io/config", "ProxyConfig.gatewayTopology", "ProxyConfig.gatewayTopology.numTrustedProxies"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{Annotations: map[string]string{
					"proxy.istio.io/config": `{"gatewayTopology":{"numTrustedProxies":1}}`,
				}})
				s.Apply(t, httpRouteTo(gw, "{{.BackendV1}}", 80))
				s.Apply(t, authzPolicy(gw, "DENY", `
  - from:
    - source:
        remoteIpBlocks: ["1.2.3.4/32"]
`))
				gw.Expect(t, Request{Headers: H("X-Forwarded-For", "1.2.3.4")}, check.Status(403))
				gw.Expect(t, Request{Headers: H("X-Forwarded-For", "5.6.7.8")}, check.OK())
			},
		},
		Feature{
			ID:          "dataplane.proxy-protocol.downstream",
			Description: "gatewayTopology.proxyProtocol makes the gateway accept (and require) a PROXY protocol header from clients (tested on a TCP listener; echo only sends PROXY headers over raw TCP).",
			Roles:       ingressRole,
			Covers:      []string{"ProxyConfig.gatewayTopology.proxyProtocol"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{
					Annotations: map[string]string{"proxy.istio.io/config": `{"gatewayTopology":{"proxyProtocol":{}}}`},
					Listeners: `
  - {name: tcp, port: 9090, protocol: TCP}
`,
				})
				s.Apply(t, `
apiVersion: gateway.networking.k8s.io/v1
kind: TCPRoute
metadata:
  name: {{.Name}}
spec:
  parentRefs: [{name: {{.GW}}}]
  rules:
  - backendRefs: [{name: {{.BackendV1}}, port: 9090}]
`, map[string]any{"GW": gw.Name})
				for _, v := range []int{1, 2} {
					gw.Expect(t, Request{Port: 9090, Scheme: scheme.TCP, Opts: func(o *echo.CallOptions) { o.ProxyProtocolVersion = v }}, check.OK())
				}
			},
		},
		Feature{
			ID:          "dataplane.proxy-protocol.upstream",
			Description: "A DestinationRule proxyProtocol setting makes the gateway send a PROXY header to the backend.",
			Roles:       ingressRole,
			Covers:      []string{"DestinationRule.trafficPolicy.proxyProtocol", "DestinationRule.trafficPolicy.proxyProtocol.version=V1"},
			Run: func(t framework.TestContext, s *Scenario) {
				// The external echo's "tcp-proxy" port (87 → 18087) expects PROXY protocol. A per-feature
				// ServiceEntry keeps the DR from affecting others.
				host := s.Name() + "." + ExternalHost
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.SEHost}}"]
  location: MESH_EXTERNAL
  resolution: DNS
  ports:
  - {number: 80, name: http, protocol: HTTP, targetPort: 87}
  endpoints:
  - address: {{.External}}.{{.ExternalNamespace}}.svc.cluster.local
---
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}
spec:
  host: {{.SEHost}}
  trafficPolicy:
    proxyProtocol:
      version: V1
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}
spec:
  parentRefs:
  - name: {{.SharedGateway}}
  hostnames: ["{{.Host}}"]
  rules:
  - backendRefs:
    - {group: networking.istio.io, kind: Hostname, name: {{.SEHost}}, port: 80}
`, s.sharedVars(), map[string]any{"SEHost": host})
				s.SharedGateway().Expect(t, Request{}, check.And(check.OK(), check.ProxyProtocolVersion("1")))
			},
		},
		Feature{
			ID:          "dataplane.xfcc.sanitize",
			Description: "A client-supplied X-Forwarded-Client-Cert is not forwarded (gateways sanitize XFCC by default).",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				// Out-of-mesh backend: an in-mesh sidecar would replace XFCC itself and hide the result.
				gw := s.NewGateway(t, GatewayOptions{})
				s.Apply(t, externalRoute(gw))
				gw.Expect(t, Request{}, FromService(ExternalSvc))
				gw.ExpectStable(t, Request{Headers: H("X-Forwarded-Client-Cert", "By=spiffe://evil/ns/x/sa/y;Hash=0")},
					check.And(check.OK(), headerNotContains("X-Forwarded-Client-Cert", "spiffe://evil")))
			},
		},
		Feature{
			ID:          "dataplane.x-forwarded-proto",
			Description: "The backend sees X-Forwarded-Proto reflecting the client's scheme.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				s.Apply(t, httpRouteToShared(s, "{{.BackendV1}}", 80), s.sharedVars())
				s.SharedGateway().Expect(t, Request{}, check.RequestHeader("X-Forwarded-Proto", "http"))
			},
		},
		Feature{
			ID:          "dataplane.request-id",
			Description: "The gateway generates an X-Request-Id (used by Istio for tracing/log correlation); as an edge proxy it replaces any client-supplied value.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				s.Apply(t, httpRouteToShared(s, "{{.BackendV1}}", 80), s.sharedVars())
				gw := s.SharedGateway()
				gw.Expect(t, Request{}, RequestHeaderPresent("X-Request-Id"))
				gw.Expect(t, Request{Headers: H("X-Request-Id", "pcs-fixed-id")}, check.And(
					RequestHeaderPresent("X-Request-Id"), headerNotContains("X-Request-Id", "pcs-fixed-id")))
			},
		},
		Feature{
			ID:          "dataplane.trace-context-propagation",
			Description: "W3C trace context from the client is propagated to the backend (same trace id).",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				s.Apply(t, httpRouteToShared(s, "{{.BackendV1}}", 80), s.sharedVars())
				traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
				s.SharedGateway().Expect(t, Request{Headers: H("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")},
					RequestHeaderContains("traceparent", traceID))
			},
		},
		Feature{
			ID:          "dataplane.host-port-ignored",
			Description: "A Host header with a port (host:port) still matches routes for the bare hostname.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				s.Apply(t, httpRouteToShared(s, "{{.BackendV1}}", 80), s.sharedVars())
				s.SharedGateway().Expect(t, Request{Host: s.Host() + ":8080"}, Version("v1"))
			},
		},
		Feature{
			ID:          "dataplane.host-case-insensitive",
			Description: "Host matching is case-insensitive.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				s.Apply(t, httpRouteToShared(s, "{{.BackendV1}}", 80), s.sharedVars())
				s.SharedGateway().Expect(t, Request{Host: upper(s.Host())}, Version("v1"))
			},
		},
		Feature{
			ID:          "dataplane.path-normalization",
			Description: "Paths are normalized (dot segments resolved) before route matching, so /x/../admin matches a /admin route.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				s.Apply(t, `
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}
spec:
  parentRefs:
  - name: {{.SharedGateway}}
  hostnames: ["{{.Host}}"]
  rules:
  - matches:
    - path: {type: Exact, value: /admin}
    backendRefs:
    - {name: {{.BackendV2}}, port: 80}
  - backendRefs:
    - {name: {{.BackendV1}}, port: 80}
`, s.sharedVars())
				s.SharedGateway().Expect(t, Request{Path: "/x/../admin"}, Version("v2"))
			},
		},
		Feature{
			ID:          "dataplane.no-route-404",
			Description: "Requests for a hostname with no route are answered with 404 by the gateway.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				s.SharedGateway().Expect(t, Request{Host: "no-route." + s.Host()}, check.Status(404))
			},
		},
		Feature{
			ID:          "dataplane.default-retry-on-connect-failure",
			Description: "Without explicit retry config, requests are retried on connection failure (Istio default retry policy), so a single unavailable endpoint does not cause errors.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				// A ServiceEntry with one live endpoint and one dead one (a closed port on another pod;
				// endpoints sharing an IP would be deduplicated).
				ip := s.Env.External[0].WorkloadsOrFail(t)[0].Address()
				deadIP := clientIP(t, s)
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.SEHost}}"]
  location: MESH_EXTERNAL
  resolution: STATIC
  ports:
  - {number: 80, name: http, protocol: HTTP}
  endpoints:
  - {address: {{.IP}}, ports: {http: 18080}}
  - {address: {{.DeadIP}}, ports: {http: 1}}
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}
spec:
  parentRefs:
  - name: {{.SharedGateway}}
  hostnames: ["{{.Host}}"]
  rules:
  - backendRefs:
    - {group: networking.istio.io, kind: Hostname, name: {{.SEHost}}, port: 80}
`, s.sharedVars(), map[string]any{"SEHost": s.Name() + "." + ExternalHost, "IP": ip, "DeadIP": deadIP})
				s.SharedGateway().Expect(t, Request{Count: 30, Opts: newConnPerRequest}, MaxFailures(1))
			},
		},
	)
}
