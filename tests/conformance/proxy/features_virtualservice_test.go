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

	"k8s.io/apimachinery/pkg/util/rand"

	echoClient "istio.io/istio/pkg/test/echo"
	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
)

// VirtualService semantics. Role-neutral features run on every L7 proxy through a Target: on ingress
// the VirtualService is bound to an Istio Gateway selecting a dedicated gateway's pods, on sidecars
// and waypoints it is a mesh VirtualService for the feature's backend Service. Features about Istio
// Gateway servers or several hostnames stay ingress-only.
func init() {
	Register(
		// --- Ported from tests/integration/pilot/common/routing.go ("VirtualService" cases) ---
		Feature{
			ID:          "istioapi.virtualservice.match-string-variants",
			Description: "VirtualService match on method, scheme and withoutHeaders with prefix and regex string matches.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"VirtualService.http.match.method.prefix", "VirtualService.http.match.method.regex", "VirtualService.http.match.scheme.prefix",
				"VirtualService.http.match.scheme.regex", "VirtualService.http.match.withoutHeaders.prefix", "VirtualService.http.match.withoutHeaders.regex",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - match: [{uri: {prefix: /method}, method: {prefix: PO}}, {uri: {prefix: /method}, method: {regex: "PU.*"}}]
    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
  - match: [{uri: {prefix: /scheme-prefix}, scheme: {prefix: ht}}, {uri: {prefix: /scheme-regex}, scheme: {regex: "h.tp"}}]
    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
  - match: [{uri: {prefix: /without-prefix}, withoutHeaders: {x-w: {prefix: ab}}}, {uri: {prefix: /without-regex}, withoutHeaders: {x-w: {regex: "a.c"}}}]
    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
  - route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
`))
				s.Case(t, "method", func(t framework.TestContext) {
					tg.Expect(t, Request{Path: "/method", Method: "POST"}, Version("v2"))
					tg.Expect(t, Request{Path: "/method", Method: "PUT"}, Version("v2"))
					tg.Expect(t, Request{Path: "/method"}, Version("v1"))
				})
				s.Case(t, "scheme", func(t framework.TestContext) {
					tg.Expect(t, Request{Path: "/scheme-prefix"}, Version("v2"))
					tg.Expect(t, Request{Path: "/scheme-regex"}, Version("v2"))
				})
				s.Case(t, "without-headers", func(t framework.TestContext) {
					tg.Expect(t, Request{Path: "/without-prefix"}, Version("v2"))
					tg.Expect(t, Request{Path: "/without-prefix", Headers: H("x-w", "abc")}, Version("v1"))
					tg.Expect(t, Request{Path: "/without-regex"}, Version("v2"))
					tg.Expect(t, Request{Path: "/without-regex", Headers: H("x-w", "abc")}, Version("v1"))
				})
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.match-source",
			Description: "VirtualService match on sourceLabels and sourceNamespace selects routes by the calling workload.",
			Roles:       []Role{RoleIngress, RoleSidecar},
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.match.sourceLabels", "VirtualService.http.match.sourceNamespace"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - match: [{uri: {prefix: /labels}, sourceLabels: {pcs-no-such-label: "x"}}]
    route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
  - match: [{uri: {prefix: /labels}, sourceLabels: { {{.SourceLabels}} }}]
    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
  - match: [{uri: {prefix: /namespace}, sourceNamespace: pcs-no-such-namespace}]
    route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
  - match: [{uri: {prefix: /namespace}, sourceNamespace: {{.SourceNamespace}}}]
    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
  - route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
`))
				s.Case(t, "source-labels", func(t framework.TestContext) {
					tg.Expect(t, Request{Path: "/labels"}, Version("v2"))
				})
				s.Case(t, "source-namespace", func(t framework.TestContext) {
					tg.Expect(t, Request{Path: "/namespace"}, Version("v2"))
				})
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.mirror-subset",
			Description: "VirtualService http.mirror to a subset on an explicit port, with mirrorPercentage value 100, copies every request to that subset.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"VirtualService.http.mirror.subset", "VirtualService.http.mirror.port", "VirtualService.http.mirror.port.number",
				"VirtualService.http.mirrorPercentage.value",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}
spec:
  host: {{.Dest}}
  subsets:
  - {name: v2, labels: {version: v2}}
`)
				s.Apply(t, tg.VirtualService(`
  - mirror: {host: {{.Dest}}, subset: v2, port: {number: 80}}
    mirrorPercentage: {value: 100}
    route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
`))
				marker := "vs-mirror-subset-" + rand.String(12)
				expectMirrored(t, tg, "v2", marker)
			},
		},
		Feature{
			ID: "istioapi.virtualservice.cors-unmatched-preflights",
			Description: "VirtualService corsPolicy.unmatchedPreflights: FORWARD sends a preflight with a non-matching origin to the backend, " +
				"IGNORE answers it at the proxy.",
			Roles:      l7Roles,
			RequiresL7: true,
			Covers: []string{
				"VirtualService.http.corsPolicy.unmatchedPreflights", "VirtualService.http.corsPolicy.unmatchedPreflights=FORWARD",
				"VirtualService.http.corsPolicy.unmatchedPreflights=IGNORE",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - match: [{uri: {prefix: /forward}}]
    corsPolicy: {allowOrigins: [{exact: "https://allowed.example.com"}], allowMethods: [GET], unmatchedPreflights: FORWARD}
    route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
  - match: [{uri: {prefix: /ignore}}]
    corsPolicy: {allowOrigins: [{exact: "https://allowed.example.com"}], allowMethods: [GET], unmatchedPreflights: IGNORE}
    route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
`))
				preflight := func(path string) Request {
					return Request{Path: path, Method: "OPTIONS", Headers: H("Origin", "https://other.example.com", "Access-Control-Request-Method", "GET")}
				}
				s.Case(t, "forward", func(t framework.TestContext) {
					tg.Expect(t, preflight("/forward"), check.And(check.OK(), fromBackend(true)))
				})
				s.Case(t, "ignore", func(t framework.TestContext) {
					// The backend is reachable (forward); an ignored preflight never gets there.
					tg.Expect(t, preflight("/forward"), fromBackend(true))
					tg.ExpectStable(t, preflight("/ignore"), fromBackend(false))
				})
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.first-match-wins",
			Description: "VirtualService http routes are evaluated in order: a catch-all route listed first shadows a more specific one listed after it.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"interaction:virtualservice.first-match-wins"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
  - match: [{uri: {prefix: /specific}}]
    route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
`))
				tg.Expect(t, Request{Path: "/specific"}, Version("v2"))
			},
		},
		// Istio merges VirtualServices for a host only when they are bound to a gateway; sidecars use
		// one of them (istio.io/latest/docs/ops/best-practices/traffic-management/).
		Feature{
			ID:          "istioapi.virtualservice.multiple-for-host",
			Description: "Two VirtualServices for the same host bound to the same gateway or waypoint are merged: routes of both apply.",
			Roles:       []Role{RoleIngress, RoleWaypoint},
			RequiresL7:  true,
			Covers:      []string{"interaction:virtualservice.multiple-for-host"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				for _, p := range []string{"a", "b"} {
					s.Apply(t, tg.VirtualService(`
  - match: [{uri: {prefix: /`+p+`}}]
    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
`), map[string]any{"Name": s.Name(p)})
				}
				tg.Expect(t, Request{Path: "/a"}, Version("v2"))
				tg.Expect(t, Request{Path: "/b"}, Version("v2"))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.headers-full",
			Description: "VirtualService request/response header add/set/remove, at both the rule and the route-destination level.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"VirtualService.http.headers.request", "VirtualService.http.headers.request.add", "VirtualService.http.headers.request.set",
				"VirtualService.http.headers.request.remove", "VirtualService.http.headers.response", "VirtualService.http.headers.response.add",
				"VirtualService.http.headers.response.set", "VirtualService.http.headers.response.remove",
				"VirtualService.http.route.headers", "VirtualService.http.route.headers.request", "VirtualService.http.route.headers.request.add",
				"VirtualService.http.route.headers.request.set", "VirtualService.http.route.headers.request.remove",
				"VirtualService.http.route.headers.response", "VirtualService.http.route.headers.response.add",
				"VirtualService.http.route.headers.response.set", "VirtualService.http.route.headers.response.remove",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - headers:
      request:
        add: {x-rule-add: a}
        set: {x-rule-set: s}
        remove: [x-rule-remove]
      response:
        add: {x-rule-resp-add: a}
        set: {x-rule-resp-set: s}
        remove: [x-rule-resp-remove]
    route:
    - destination: {host: {{.DestV1}}, port: {number: 80}}
      headers:
        request:
          add: {x-route-add: a}
          set: {x-route-set: s}
          remove: [x-route-remove]
        response:
          add: {x-route-resp-add: a}
          set: {x-route-resp-set: s}
          remove: [x-route-resp-remove]
`))
				// echo returns response headers requested via ?headers=name:value.
				tg.Expect(t, Request{
					Path:    "/?headers=x-rule-resp-remove:x,x-route-resp-remove:x,x-rule-resp-set:orig,x-route-resp-set:orig",
					Headers: H("x-rule-remove", "x", "x-route-remove", "x", "x-rule-set", "orig", "x-route-set", "orig"),
				}, check.And(
					check.RequestHeader("x-rule-add", "a"), check.RequestHeader("x-rule-set", "s"), RequestHeaderAbsent("x-rule-remove"),
					check.RequestHeader("x-route-add", "a"), check.RequestHeader("x-route-set", "s"), RequestHeaderAbsent("x-route-remove"),
					check.ResponseHeader("x-rule-resp-add", "a"), check.ResponseHeader("x-rule-resp-set", "s"), ResponseHeaderAbsent("x-rule-resp-remove"),
					check.ResponseHeader("x-route-resp-add", "a"), check.ResponseHeader("x-route-resp-set", "s"), ResponseHeaderAbsent("x-route-resp-remove"),
				))
			},
		},
		Feature{
			ID: "istioapi.virtualservice.match-more",
			Description: "VirtualService match on authority (exact/prefix/regex), uri prefix with ignoreUriCase, header exact/regex, " +
				"queryParams prefix/regex, scheme, port, gateways and withoutHeaders exact.",
			Roles:      l7Roles,
			RequiresL7: true,
			Covers: []string{
				"VirtualService.http.match", "VirtualService.http.match.uri.prefix",
				"VirtualService.http.match.ignoreUriCase", "VirtualService.http.match.headers.exact", "VirtualService.http.match.headers.regex",
				"VirtualService.http.match.queryParams.exact", "VirtualService.http.match.queryParams.prefix", "VirtualService.http.match.queryParams.regex",
				"VirtualService.http.match.scheme", "VirtualService.http.match.scheme.exact", "VirtualService.http.match.port",
				"VirtualService.http.match.gateways", "VirtualService.http.match.withoutHeaders.exact", "VirtualService.http.match.method.exact",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - match: [{uri: {prefix: /CaSe}, ignoreUriCase: true}]
    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
  - match: [{headers: {x-e: {exact: "1"}}}, {headers: {x-r: {regex: "v[0-9]+"}}}]
    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
  - match: [{queryParams: {qe: {exact: "1"}}}, {queryParams: {qp: {prefix: ab}}}, {queryParams: {qr: {regex: "[0-9]+"}}}]
    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
  - match: [{uri: {prefix: /scheme}, scheme: {exact: http}, port: 80, gateways: ["{{.TargetGateway}}"], method: {exact: GET}}]
    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
  - match: [{uri: {prefix: /without}, withoutHeaders: {x-w: {exact: "1"}}}]
    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
  - route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
`))
				h := tg.Host()
				tg.Expect(t, Request{Host: h, Path: "/case/x"}, Version("v2"))
				tg.Expect(t, Request{Host: h, Headers: H("x-e", "1")}, Version("v2"))
				tg.Expect(t, Request{Host: h, Headers: H("x-r", "v12")}, Version("v2"))
				tg.Expect(t, Request{Host: h, Path: "/?qe=1"}, Version("v2"))
				tg.Expect(t, Request{Host: h, Path: "/?qp=abc"}, Version("v2"))
				tg.Expect(t, Request{Host: h, Path: "/?qr=42"}, Version("v2"))
				tg.Expect(t, Request{Host: h, Path: "/scheme"}, Version("v2"))
				tg.Expect(t, Request{Host: h, Path: "/without"}, Version("v2"))
				tg.Expect(t, Request{Host: h, Path: "/without", Headers: H("x-w", "1")}, Version("v1"))
				tg.Expect(t, Request{Host: h, Path: "/other"}, Version("v1"))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.match-authority",
			Description: "VirtualService authority matches (exact, prefix, regex) select routes within a multi-host VirtualService.",
			Roles:       ingressRole,
			Covers: []string{
				"VirtualService.http.match.authority", "VirtualService.http.match.authority.exact",
				"VirtualService.http.match.authority.prefix", "VirtualService.http.match.authority.regex",
			},
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
    hosts: ["*.{{.Host}}"]
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["*.{{.Host}}"]
  gateways: ["{{.Name}}-istio"]
  http:
  - match: [{authority: {exact: "exact.{{.Host}}"}}, {authority: {prefix: "pre"}}, {authority: {regex: "re[0-9]+\\..*"}}]
    route: [{destination: {host: {{.BackendV2}}, port: {number: 80}}}]
  - route: [{destination: {host: {{.BackendV1}}, port: {number: 80}}}]
`, map[string]any{"GW": gw.Name})
				gw.Expect(t, Request{Host: "exact." + s.Host()}, Version("v2"))
				gw.Expect(t, Request{Host: "prefix." + s.Host()}, Version("v2"))
				gw.Expect(t, Request{Host: "re7." + s.Host()}, Version("v2"))
				gw.Expect(t, Request{Host: "other." + s.Host()}, Version("v1"))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.redirect-more",
			Description: "VirtualService redirect scheme, explicit port, derivePort, and prefixRewrite.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"VirtualService.http.redirect.scheme", "VirtualService.http.redirect.port", "VirtualService.http.redirect.prefixRewrite",
				"VirtualService.http.redirect.derivePort", "VirtualService.http.redirect.derivePort=FROM_PROTOCOL_DEFAULT",
				"VirtualService.http.redirect.derivePort=FROM_REQUEST_PORT",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - match: [{uri: {prefix: /port}}]
    redirect: {scheme: https, port: 8443}
  - match: [{uri: {prefix: /derive-default}}]
    redirect: {scheme: https, derivePort: FROM_PROTOCOL_DEFAULT}
  - match: [{uri: {prefix: /derive-request}}]
    redirect: {derivePort: FROM_REQUEST_PORT}
  - match: [{uri: {prefix: /old}}]
    redirect: {prefixRewrite: /new}
`))
				h := tg.Host()
				tg.Expect(t, Request{Host: h, Path: "/port"}, ResponseHeaderContains("Location", "https://"+h+":8443/port"))
				tg.Expect(t, Request{Host: h, Path: "/derive-default"}, check.ResponseHeader("Location", "https://"+h+"/derive-default"))
				// The request port is 80, the default for http, so it may be omitted from Location.
				tg.Expect(t, Request{Host: h, Path: "/derive-request"}, check.Or(
					check.ResponseHeader("Location", "http://"+h+":80/derive-request"),
					check.ResponseHeader("Location", "http://"+h+"/derive-request")))
				tg.Expect(t, Request{Host: h, Path: "/old/x"}, ResponseHeaderContains("Location", "/new/x"))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.rewrite-more",
			Description: "VirtualService rewrite of authority and regex URI rewrite.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"VirtualService.http.rewrite.authority", "VirtualService.http.rewrite.uriRegexRewrite",
				"VirtualService.http.rewrite.uriRegexRewrite.match", "VirtualService.http.rewrite.uriRegexRewrite.rewrite",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - match: [{uri: {prefix: /re/}}]
    rewrite:
      authority: rewritten.example.com
      uriRegexRewrite: {match: "^/re/(.*)$", rewrite: "/new/\\1"}
    route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
`))
				tg.Expect(t, Request{Path: "/re/x"}, check.And(Path("/new/x"), BackendHost("rewritten.example.com")))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.cors-full",
			Description: "VirtualService corsPolicy origin prefix/regex, allowHeaders, exposeHeaders, maxAge and allowCredentials in preflight and simple responses.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"VirtualService.http.corsPolicy.allowOrigins.exact", "VirtualService.http.corsPolicy.allowOrigins.prefix",
				"VirtualService.http.corsPolicy.allowOrigins.regex", "VirtualService.http.corsPolicy.allowHeaders",
				"VirtualService.http.corsPolicy.exposeHeaders", "VirtualService.http.corsPolicy.maxAge", "VirtualService.http.corsPolicy.allowCredentials",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - corsPolicy:
      allowOrigins:
      - exact: "https://exact.example.com"
      - prefix: "https://prefix."
      - regex: "https://re[0-9]+\\.example\\.com"
      allowMethods: [GET, POST]
      allowHeaders: [x-custom]
      exposeHeaders: [x-exposed]
      maxAge: 1h
      allowCredentials: true
    route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
`))
				h := tg.Host()
				preflight := func(origin string) Request {
					return Request{Host: h, Method: "OPTIONS", Headers: H("Origin", origin, "Access-Control-Request-Method", "POST",
						"Access-Control-Request-Headers", "x-custom")}
				}
				tg.Expect(t, preflight("https://exact.example.com"), check.And(
					check.ResponseHeader("Access-Control-Allow-Origin", "https://exact.example.com"),
					ResponseHeaderContains("Access-Control-Allow-Headers", "x-custom"),
					check.ResponseHeader("Access-Control-Max-Age", "3600"),
					check.ResponseHeader("Access-Control-Allow-Credentials", "true")))
				tg.Expect(t, preflight("https://prefix.example.com"), check.ResponseHeader("Access-Control-Allow-Origin", "https://prefix.example.com"))
				tg.Expect(t, preflight("https://re7.example.com"), check.ResponseHeader("Access-Control-Allow-Origin", "https://re7.example.com"))
				tg.Expect(t, Request{Host: h, Headers: H("Origin", "https://exact.example.com")},
					ResponseHeaderContains("Access-Control-Expose-Headers", "x-exposed"))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.mirrors",
			Description: "VirtualService mirrors (multiple mirror destinations with percentage, port and subset).",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"VirtualService.http.mirrors", "VirtualService.http.mirrors.destination", "VirtualService.http.mirrors.destination.host",
				"VirtualService.http.mirrors.destination.port", "VirtualService.http.mirrors.destination.port.number",
				"VirtualService.http.mirrors.destination.subset", "VirtualService.http.mirrors.percentage", "VirtualService.http.mirrors.percentage.value",
			},
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
  - {name: v2, labels: {version: v2}}
`)
				s.Apply(t, tg.VirtualService(`
  - mirrors:
    - destination: {host: {{.Dest}}, subset: v2, port: {number: 80}}
      percentage: {value: 100}
    route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
`))
				marker := "vs-mirrors-" + rand.String(12)
				expectMirrored(t, tg, "v2", marker)
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.per-try-timeout",
			Description: "VirtualService retries.perTryTimeout bounds each attempt; slow attempts fail with 504 after retries.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.retries.perTryTimeout"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - retries: {attempts: 2, perTryTimeout: 500ms, retryOn: "5xx,reset,connect-failure"}
    route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
`))
				tg.Expect(t, Request{Path: "/?delay=2s"}, check.Status(504))
				tg.Expect(t, Request{Path: "/?delay=10ms"}, check.OK())
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.delegate",
			Description: "VirtualService delegation: a root VirtualService delegates a path prefix to a child VirtualService.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.delegate", "VirtualService.http.delegate.name", "VirtualService.http.delegate.namespace"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}-child
spec:
  http:
  - route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
`)
				s.Apply(t, tg.VirtualService(`
  - match: [{uri: {prefix: /delegated}}]
    delegate: {name: {{.Name}}-child, namespace: {{.Namespace}}}
  - route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
`))
				tg.Expect(t, Request{Path: "/delegated/x"}, Version("v2"))
				tg.Expect(t, Request{Path: "/other"}, Version("v1"))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.weights",
			Description: "VirtualService route weights split traffic proportionally.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"VirtualService.http.route.weight", "VirtualService.http.route.destination.host",
				"VirtualService.http.route.destination.port", "VirtualService.http.route.destination.port.number",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - route:
    - {destination: {host: {{.DestV1}}, port: {number: 80}}, weight: 80}
    - {destination: {host: {{.DestV2}}, port: {number: 80}}, weight: 20}
`))
				tg.Expect(t, Request{Count: SampleSize(80, 20)}, Distribution(map[string]int{"v1": 80, "v2": 20}))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.export-to",
			Description: "A VirtualService with exportTo '.' in another namespace is not visible to the gateway; without it, it is.",
			Roles:       ingressRole,
			Covers:      []string{"VirtualService.exportTo"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := istioGatewayBinding(t, s)
				ext := s.Env.ExternalNamespace.Name()
				for _, c := range []struct{ name, host, export string }{
					{s.Name("private"), "vs.private-" + s.Host(), `exportTo: ["."]`},
					{s.Name("public"), "vs." + s.Host(), ``},
				} {
					s.ApplyTo(t, ext, `
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.N}}
spec:
  hosts: ["{{.H}}"]
  gateways: ["{{.Namespace}}/{{.Name}}-istio"]
  {{.E}}
  http:
  - route: [{destination: {host: {{.External}}.{{.ExternalNamespace}}.svc.cluster.local, port: {number: 80}}}]
`, map[string]any{"N": c.name, "H": c.host, "E": c.export})
				}
				gw.Expect(t, Request{Host: "vs." + s.Host()}, FromService(ExternalSvc))
				gw.ExpectStable(t, Request{Host: "vs.private-" + s.Host()}, check.Status(404))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.tls-sni-passthrough",
			Description: "An Istio Gateway TLS PASSTHROUGH server with a VirtualService tls route forwards by SNI without terminating TLS.",
			Roles:       ingressRole,
			Covers: []string{
				"Gateway.servers.tls.mode=PASSTHROUGH", "VirtualService.tls", "VirtualService.tls.match", "VirtualService.tls.match.sniHosts",
				"VirtualService.tls.match.port", "VirtualService.tls.route", "VirtualService.tls.route.destination",
				"VirtualService.tls.route.destination.host", "VirtualService.tls.route.destination.port", "VirtualService.tls.route.destination.port.number",
			},
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
  - port: {number: 9443, name: tls, protocol: TLS}
    hosts: ["{{.SNI}}"]
    tls: {mode: PASSTHROUGH}
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.SNI}}"]
  gateways: ["{{.Name}}-istio"]
  tls:
  - match: [{sniHosts: ["{{.SNI}}"], port: 9443}]
    route:
    - destination: {host: {{.External}}.{{.ExternalNamespace}}.svc.cluster.local, port: {number: 443}}
`, map[string]any{"GW": gw.Name, "SNI": BackendTLSHost})
				gw.Expect(t, Request{Address: gw.PodIP(t), Host: BackendTLSHost, Port: 9443, Scheme: scheme.HTTPS, Opts: tlsClient(BackendTLSHost, echoTestCA())},
					check.And(FromService(ExternalSvc), check.SNI(BackendTLSHost)))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.tcp-match-weight",
			Description: "VirtualService tcp match on port and weighted tcp routes.",
			Roles:       ingressRole,
			Covers: []string{
				"VirtualService.tcp.match", "VirtualService.tcp.match.port", "VirtualService.tcp.route.weight",
				"VirtualService.tcp.route.destination.host", "VirtualService.tcp.route.destination.port", "VirtualService.tcp.route.destination.port.number",
			},
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
  - match: [{port: 9999}]
    route:
    - {destination: {host: {{.BackendV1}}, port: {number: 9090}}, weight: 50}
    - {destination: {host: {{.BackendV2}}, port: {number: 9090}}, weight: 50}
`, map[string]any{"GW": gw.Name})
				gw.Expect(t, Request{Address: gw.PodIP(t), Port: 9999, Scheme: scheme.TCP, Count: SampleSize(50, 50), Opts: newConnPerRequest},
					Distribution(map[string]int{"v1": 50, "v2": 50}))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.fault-percentages",
			Description: "VirtualService fault abort percentage applies to roughly that share of requests.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.fault.abort.percentage.value"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - fault:
      abort: {httpStatus: 418, percentage: {value: 50}}
    route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
`))
				tg.Expect(t, Request{Count: SampleSize(50)}, statusShare(418, 50))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.direct-response-bytes",
			Description: "VirtualService directResponse with a binary (bytes) body.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.directResponse.body.bytes"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				// base64 of "pcs-bytes"
				s.Apply(t, tg.VirtualService(`
  - directResponse: {status: 200, body: {bytes: "cGNzLWJ5dGVz"}}
`))
				tg.Expect(t, Request{}, check.And(check.Status(200), check.BodyContains("pcs-bytes")))
			},
		},
	)
}

// fromBackend checks whether responses were produced by the echo backend (it reports its hostname)
// or by the proxy itself.
func fromBackend(want bool) echo.Checker {
	return check.Each(func(r echoClient.Response) error {
		if got := r.Hostname != ""; got != want {
			return fmt.Errorf("expected response from backend=%v, got hostname %q (code %s)", want, r.Hostname, r.Code)
		}
		return nil
	})
}
