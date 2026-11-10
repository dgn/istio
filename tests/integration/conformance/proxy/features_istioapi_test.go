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
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/util/rand"

	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo/check"
	"istio.io/istio/pkg/test/util/retry"
	"istio.io/istio/tests/common/jwt"
)

// Istio networking APIs (networking.istio.io Gateway + VirtualService) bound to a gateway that was
// deployed through Gateway API, by selecting its pods with the standard gateway-name label. This is
// how users get VirtualService-only features (subsets, fault injection, claim-based routing) on
// Gateway API deployed gateways. Plus Envoy-specific extension APIs, catalogued so the matrix shows
// what is lost when switching implementations.
func init() {
	Register(
		Feature{
			ID:          "istioapi.gateway-virtualservice.route",
			Description: "An Istio Gateway selecting the gateway pods plus a VirtualService routes traffic.",
			Roles:       ingressRole,
			Covers:      []string{"Gateway.selector", "Gateway.servers", "Gateway.servers.port", "Gateway.servers.hosts", "VirtualService.hosts", "VirtualService.gateways", "VirtualService.http", "VirtualService.http.route", "VirtualService.http.route.destination"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := istioGatewayBinding(t, s)
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["vs.{{.Host}}"]
  gateways: ["{{.Name}}-istio"]
  http:
  - route:
    - destination: {host: {{.BackendV2}}, port: {number: 80}}
`)
				gw.Expect(t, Request{Host: "vs." + s.Host()}, Version("v2"))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.subsets",
			Description: "VirtualService weighted routing to DestinationRule subsets.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"DestinationRule.subsets", "DestinationRule.subsets.name", "DestinationRule.subsets.labels", "VirtualService.http.route.destination.subset", "VirtualService.http.match.headers"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}
spec:
  host: {{.Dest}}
  exportTo: ["."]
  subsets:
  - {name: v1, labels: {version: v1}}
  - {name: v2, labels: {version: v2}}
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.TargetHost}}"]
  gateways: ["{{.TargetGateway}}"]
  http:
  - match: [{headers: {x-subset: {exact: v2}}}]
    route:
    - destination: {host: {{.Dest}}, subset: v2, port: {number: 80}}
  - route:
    - destination: {host: {{.Dest}}, subset: v1, port: {number: 80}}
`)
				tg.Expect(t, Request{Headers: H("x-subset", "v2")}, Version("v2"))
				tg.Expect(t, Request{}, Version("v1"))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.fault-abort",
			Description: "VirtualService fault.abort returns the configured status for the configured percentage of requests.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.fault", "VirtualService.http.fault.abort", "VirtualService.http.fault.abort.httpStatus", "VirtualService.http.fault.abort.percentage"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.TargetHost}}"]
  gateways: ["{{.TargetGateway}}"]
  http:
  - fault:
      abort:
        httpStatus: 418
        percentage: {value: 100}
    route:
    - destination: {host: {{.DestV1}}, port: {number: 80}}
`)
				tg.Expect(t, Request{}, check.Status(418))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.fault-delay",
			Description: "VirtualService fault.delay delays requests by the configured duration.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.fault.delay", "VirtualService.http.fault.delay.fixedDelay"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.TargetHost}}"]
  gateways: ["{{.TargetGateway}}"]
  http:
  - match: [{headers: {x-pcs-control: {exact: "1"}}}]
    route:
    - destination: {host: {{.DestV1}}, port: {number: 80}}
  - fault:
      delay:
        fixedDelay: 2s
        percentage: {value: 100}
    route:
    - destination: {host: {{.DestV1}}, port: {number: 80}}
`)
				// Two-sided: the delayed route takes at least the delay, and a control route of the
				// same VirtualService, timed in the same attempt, is well below it. Without the control
				// a slow environment could pass the lower bound with no delay configured.
				tg.Expect(t, Request{}, check.OK())
				timed := func(h http.Header) (time.Duration, error) {
					start := time.Now()
					_, err := tg.Call(t, Request{Headers: h})
					return time.Since(start), err
				}
				retry.UntilSuccessOrFail(t, func() error {
					control, err := timed(H("x-pcs-control", "1"))
					if err != nil {
						return err
					}
					delayed, err := timed(nil)
					if err != nil {
						return err
					}
					if delayed < 1800*time.Millisecond || control > time.Second {
						return fmt.Errorf("delayed request took %v (want >= 2s), control took %v (want < 1s)", delayed, control)
					}
					return nil
				}, retry.Timeout(time.Minute))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.header-and-rewrite",
			Description: "VirtualService headers manipulation and URI rewrite apply on the gateway.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.headers", "VirtualService.http.rewrite", "VirtualService.http.rewrite.uri", "VirtualService.http.match.uri"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.TargetHost}}"]
  gateways: ["{{.TargetGateway}}"]
  http:
  - match: [{uri: {prefix: /old}}]
    rewrite: {uri: /new}
    headers:
      request:
        set: {x-vs: "yes"}
      response:
        add: {x-vs-resp: "yes"}
    route:
    - destination: {host: {{.DestV1}}, port: {number: 80}}
`)
				tg.Expect(t, Request{Path: "/old/x"}, check.And(
					Path("/new/x"), check.RequestHeader("x-vs", "yes"), check.ResponseHeader("x-vs-resp", "yes")))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.claim-routing",
			Description: "VirtualService header match on @request.auth.claims routes by validated JWT claims.",
			Roles:       []Role{RoleIngress, RoleWaypoint},
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.match.headers"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.RequestAuthentication(""))
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.TargetHost}}"]
  gateways: ["{{.TargetGateway}}"]
  http:
  - match: [{headers: {"@request.auth.claims.sub": {exact: sub-1}}}]
    route:
    - destination: {host: {{.DestV2}}, port: {number: 80}}
  - route:
    - destination: {host: {{.DestV1}}, port: {number: 80}}
`)
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1)}, Version("v2"))
				tg.Expect(t, Request{}, Version("v1"))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.cors",
			Description: "VirtualService corsPolicy answers preflight requests for allowed origins.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.corsPolicy", "VirtualService.http.corsPolicy.allowOrigins", "VirtualService.http.corsPolicy.allowMethods"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.TargetHost}}"]
  gateways: ["{{.TargetGateway}}"]
  http:
  - corsPolicy:
      allowOrigins: [{exact: "https://allowed.example.com"}]
      allowMethods: [GET, POST]
    route:
    - destination: {host: {{.DestV1}}, port: {number: 80}}
`)
				tg.Expect(t, Request{Method: "OPTIONS", Headers: H(
					"Origin", "https://allowed.example.com", "Access-Control-Request-Method", "POST")},
					check.ResponseHeader("Access-Control-Allow-Origin", "https://allowed.example.com"))
			},
		},
		Feature{
			ID:          "impl-specific.envoyfilter",
			Description: "[Envoy-specific extension API] An EnvoyFilter selecting the gateway pods patches its config (adds a response header).",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := policyGateway(t, s)
				s.Apply(t, `
apiVersion: networking.istio.io/v1alpha3
kind: EnvoyFilter
metadata:
  name: {{.Name}}
spec:
  workloadSelector:
    labels:
      gateway.networking.k8s.io/gateway-name: {{.GW}}
  configPatches:
  - applyTo: ROUTE_CONFIGURATION
    match: {context: GATEWAY}
    patch:
      operation: MERGE
      value:
        response_headers_to_add:
        - header: {key: x-envoyfilter, value: "yes"}
`, map[string]any{"GW": gw.Name})
				gw.Expect(t, Request{}, check.ResponseHeader("x-envoyfilter", "yes"))
			},
		},
		Feature{
			ID:          "impl-specific.trafficextension-lua",
			Description: "[Envoy-specific extension API] A Lua TrafficExtension targeting the gateway runs (adds a response header).",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := policyGateway(t, s)
				s.Apply(t, `
apiVersion: extensions.istio.io/v1alpha1
kind: TrafficExtension
metadata:
  name: {{.Name}}
spec:
  phase: STATS
  targetRefs:
  - {kind: Gateway, group: gateway.networking.k8s.io, name: {{.GW}}}
  lua:
    inlineCode: |
      function envoy_on_response(response_handle)
        response_handle:headers():add("x-lua", "yes")
      end
`, map[string]any{"GW": gw.Name})
				gw.Expect(t, Request{}, check.ResponseHeader("x-lua", "yes"))
			},
		},
	)
}

// istioGatewayBinding creates a dedicated Gateway API gateway plus an Istio Gateway named
// "<Name>-istio" selecting its pods on port 80 for "vs.<Host>".
func istioGatewayBinding(t framework.TestContext, s *Scenario) *Gateway {
	gw := s.NewGateway(t, GatewayOptions{})
	s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: {{.Name}}-istio
spec:
  selector:
    gateway.networking.k8s.io/gateway-name: {{.GW}}
  servers:
  - port: {number: 80, name: http, protocol: HTTP}
    hosts: ["vs.{{.Host}}"]
`, map[string]any{"GW": gw.Name})
	return gw
}

// More VirtualService / Istio Gateway behaviors.
func init() {
	Register(
		Feature{
			ID:          "istioapi.virtualservice.timeout",
			Description: "VirtualService http.timeout fails slow requests with 504.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.timeout"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - timeout: 1s
    route:
    - destination: {host: {{.DestV1}}, port: {number: 80}}
`))
				tg.Expect(t, Request{Path: "/?delay=3s"}, check.Status(504))
				tg.Expect(t, Request{Path: "/?delay=10ms"}, check.OK())
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.retries",
			Description: "VirtualService http.retries (attempts, retryOn) retries failed requests.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.retries", "VirtualService.http.retries.attempts", "VirtualService.http.retries.retryOn"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - retries:
      attempts: 4
      retryOn: 5xx
    route:
    - destination: {host: {{.Dest}}, port: {number: 80}}
`))
				tg.Expect(t, Request{Count: 50, Path: "/?codes=503:1,200:3"}, MaxFailures(2))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.redirect",
			Description: "VirtualService http.redirect returns a redirect with the configured authority, uri and code.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.redirect", "VirtualService.http.redirect.uri", "VirtualService.http.redirect.authority", "VirtualService.http.redirect.redirectCode"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - redirect:
      authority: redirected.example.com
      uri: /new
      redirectCode: 308
`))
				tg.Expect(t, Request{Path: "/old"}, check.And(
					check.Status(308), ResponseHeaderContains("Location", "redirected.example.com/new")))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.direct-response",
			Description: "VirtualService http.directResponse answers with the configured status and body without a backend.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.directResponse", "VirtualService.http.directResponse.status", "VirtualService.http.directResponse.body", "VirtualService.http.directResponse.body.string"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - directResponse:
      status: 418
      body:
        string: pcs-direct
`))
				tg.Expect(t, Request{}, check.And(check.Status(418), check.BodyContains("pcs-direct")))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.mirror",
			Description: "VirtualService http.mirror with mirrorPercentage copies requests to the mirror destination.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.mirror", "VirtualService.http.mirror.host", "VirtualService.http.mirrorPercentage"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - mirror: {host: {{.DestV2}}, port: {number: 80}}
    mirrorPercentage: {value: 100}
    route:
    - destination: {host: {{.DestV1}}, port: {number: 80}}
`))
				marker := "vs-mirror-" + rand.String(12)
				tg.Expect(t, Request{Path: "/" + marker, Count: 5}, Version("v1"))
				expectMirrored(t, tg.BackendInstances(), "v2", marker)
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.match-variants",
			Description: "VirtualService match on uri regex/exact, method, queryParams, header prefix and withoutHeaders.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{"VirtualService.http.match.uri.regex", "VirtualService.http.match.uri.exact", "VirtualService.http.match.method",
				"VirtualService.http.match.queryParams", "VirtualService.http.match.headers.prefix", "VirtualService.http.match.withoutHeaders"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - match: [{uri: {regex: "/re[0-9]+"}}]
    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
  - match: [{uri: {exact: /exact}, method: {exact: POST}}]
    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
  - match: [{queryParams: {v: {exact: two}}}]
    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
  - match: [{headers: {x-pre: {prefix: abc}}}]
    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
  - match: [{withoutHeaders: {x-required: {}}}]
    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
  - route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
`))
				h := tg.Host()
				req := H("x-required", "1")
				tg.Expect(t, Request{Host: h, Headers: req, Path: "/re42"}, Version("v2"))
				tg.Expect(t, Request{Host: h, Headers: req, Path: "/exact", Method: "POST"}, Version("v2"))
				tg.Expect(t, Request{Host: h, Headers: req, Path: "/exact"}, Version("v1"))
				tg.Expect(t, Request{Host: h, Headers: req, Path: "/?v=two"}, Version("v2"))
				tg.Expect(t, Request{Host: h, Headers: H("x-required", "1", "x-pre", "abcdef")}, Version("v2"))
				tg.Expect(t, Request{Host: h}, Version("v2"))
				tg.Expect(t, Request{Host: h, Headers: req}, Version("v1"))
			},
		},
		Feature{
			ID:          "istioapi.gateway.https-redirect",
			Description: "Istio Gateway server tls.httpsRedirect answers plaintext requests with a 301 to https.",
			Roles:       ingressRole,
			Covers:      []string{"Gateway.servers.tls", "Gateway.servers.tls.httpsRedirect"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: {{.Name}}-istio
spec:
  selector:
    gateway.networking.k8s.io/gateway-name: {{.GW}}
  servers:
  - port: {number: 80, name: http, protocol: HTTP}
    hosts: ["vs.{{.Host}}"]
    tls:
      httpsRedirect: true
`, map[string]any{"GW": gw.Name})
				gw.Expect(t, Request{Host: "vs." + s.Host()}, check.And(check.Status(301), ResponseHeaderContains("Location", "https://vs."+s.Host())))
			},
		},
		Feature{
			ID:          "istioapi.gateway.tls-simple",
			Description: "Istio Gateway HTTPS server with tls.mode SIMPLE and credentialName terminates TLS on the gateway pods.",
			Roles:       ingressRole,
			Covers:      []string{"Gateway.servers.tls.mode=SIMPLE", "Gateway.servers.tls.credentialName", "Gateway.servers.port.protocol"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				// Istio Gateway credentialName secrets require the proxy to be allowed to read them.
				gw.GrantSecretRead(t)
				host := "vs." + s.Host()
				c := newCerts(t, host, host)
				s.TLSSecret(t, s.Name("cred"), c)
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: {{.Name}}-istio
spec:
  selector:
    gateway.networking.k8s.io/gateway-name: {{.GW}}
  servers:
  - port: {number: 8443, name: https, protocol: HTTPS}
    hosts: ["vs.{{.Host}}"]
    tls:
      mode: SIMPLE
      credentialName: {{.Cred}}
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["vs.{{.Host}}"]
  gateways: ["{{.Name}}-istio"]
  http:
  - route:
    - destination: {host: {{.BackendV1}}, port: {number: 80}}
`, map[string]any{"GW": gw.Name, "Cred": s.Name("cred")})
				// Port 8443 is not exposed on the generated Service; address the pod.
				gw.Expect(t, Request{Address: gw.PodIP(t), Host: host, Port: 8443, Scheme: scheme.HTTPS, Opts: tlsClient(host, c.CA)}, Version("v1"))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.tcp",
			Description: "An Istio Gateway TCP server with a VirtualService tcp route forwards raw TCP.",
			Roles:       ingressRole,
			Covers:      []string{"VirtualService.tcp", "VirtualService.tcp.route", "VirtualService.tcp.route.destination"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: {{.Name}}-istio
spec:
  selector:
    gateway.networking.k8s.io/gateway-name: {{.GW}}
  servers:
  - port: {number: 9999, name: tcp, protocol: TCP}
    hosts: ["*"]
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["*"]
  gateways: ["{{.Name}}-istio"]
  tcp:
  - route:
    - destination: {host: {{.BackendV1}}, port: {number: 9090}}
`, map[string]any{"GW": gw.Name})
				gw.Expect(t, Request{Address: gw.PodIP(t), Port: 9999, Scheme: scheme.TCP}, check.OK())
			},
		},
	)
}
