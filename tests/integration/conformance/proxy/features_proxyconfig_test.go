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
	"net/textproto"

	echoClient "istio.io/istio/pkg/test/echo"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
)

// ProxyConfig proxyHeaders and connectionSettings, applied per gateway via the proxy.istio.io/config
// annotation. Header-related features route to the out-of-mesh backend, so that the backend sees the
// request exactly as the gateway sent it (an in-mesh sidecar would add or rewrite some of these headers).
func init() {
	Register(
		// ---- proxyHeaders ----
		Feature{
			ID:          "proxyconfig.proxyheaders.server-value",
			Description: "proxyHeaders.server.value sets the Server response header.",
			Roles:       ingressRole,
			Covers:      []string{"ProxyConfig.proxyHeaders", "ProxyConfig.proxyHeaders.server", "ProxyConfig.proxyHeaders.server.value"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := proxyConfigGateway(t, s, `{"proxyHeaders":{"server":{"value":"pcs-server"}}}`, true)
				gw.Expect(t, Request{}, check.ResponseHeader("Server", "pcs-server"))
			},
		},
		Feature{
			ID:          "proxyconfig.proxyheaders.server-disabled",
			Description: "proxyHeaders.server.disabled stops the gateway from setting a Server response header.",
			Roles:       ingressRole,
			Covers:      []string{"ProxyConfig.proxyHeaders.server.disabled"},
			Run: func(t framework.TestContext, s *Scenario) {
				expectBaselineHeader(t, s, "Server", true)
				// The echo backend does not set a Server header itself.
				gw := proxyConfigGateway(t, s, `{"proxyHeaders":{"server":{"disabled":true}}}`, true)
				gw.ExpectStable(t, Request{}, check.And(check.OK(), ResponseHeaderAbsent("Server")))
			},
		},
		Feature{
			ID:          "proxyconfig.proxyheaders.request-id-disabled",
			Description: "proxyHeaders.requestId.disabled stops the gateway from generating X-Request-Id.",
			Roles:       ingressRole,
			Covers:      []string{"ProxyConfig.proxyHeaders.requestId", "ProxyConfig.proxyHeaders.requestId.disabled"},
			Run: func(t framework.TestContext, s *Scenario) {
				expectBaselineHeader(t, s, "X-Request-Id", false)
				gw := proxyConfigGateway(t, s, `{"proxyHeaders":{"requestId":{"disabled":true}}}`, true)
				gw.ExpectStable(t, Request{}, RequestHeaderAbsent("X-Request-Id"))
			},
		},
		Feature{
			ID:          "proxyconfig.proxyheaders.attempt-count-disabled",
			Description: "proxyHeaders.attemptCount.disabled stops the gateway from sending an attempt-count header upstream.",
			Roles:       ingressRole,
			Covers:      []string{"ProxyConfig.proxyHeaders.attemptCount", "ProxyConfig.proxyHeaders.attemptCount.disabled"},
			Run: func(t framework.TestContext, s *Scenario) {
				expectBaselineHeader(t, s, "X-Envoy-Attempt-Count", false)
				gw := proxyConfigGateway(t, s, `{"proxyHeaders":{"attemptCount":{"disabled":true}}}`, true)
				gw.ExpectStable(t, Request{}, RequestHeaderAbsent("X-Envoy-Attempt-Count"))
			},
		},
		Feature{
			ID:          "proxyconfig.proxyheaders.debug-headers-disabled",
			Description: "proxyHeaders.envoyDebugHeaders.disabled stops the gateway from adding debug headers (e.g. upstream service time) to responses.",
			Roles:       ingressRole,
			Covers:      []string{"ProxyConfig.proxyHeaders.envoyDebugHeaders", "ProxyConfig.proxyHeaders.envoyDebugHeaders.disabled"},
			Run: func(t framework.TestContext, s *Scenario) {
				expectBaselineHeader(t, s, "X-Envoy-Upstream-Service-Time", true)
				gw := proxyConfigGateway(t, s, `{"proxyHeaders":{"envoyDebugHeaders":{"disabled":true}}}`, true)
				gw.ExpectStable(t, Request{}, check.And(check.OK(), ResponseHeaderAbsent("X-Envoy-Upstream-Service-Time")))
			},
		},
		Feature{
			ID:          "proxyconfig.proxyheaders.x-forwarded-port-host",
			Description: "proxyHeaders.xForwardedPort and xForwardedHost add X-Forwarded-Port and X-Forwarded-Host.",
			Roles:       ingressRole,
			Covers: []string{"ProxyConfig.proxyHeaders.xForwardedPort", "ProxyConfig.proxyHeaders.xForwardedPort.enabled",
				"ProxyConfig.proxyHeaders.xForwardedHost", "ProxyConfig.proxyHeaders.xForwardedHost.enabled"},
			Run: func(t framework.TestContext, s *Scenario) {
				// X-Forwarded-Host carries the original host when the route rewrites it.
				gw := proxyConfigGateway(t, s, `{"proxyHeaders":{"xForwardedPort":{"enabled":true},"xForwardedHost":{"enabled":true}}}`, false)
				s.Apply(t, externalServiceEntry(80, "HTTP")+`---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}
spec:
  parentRefs: [{name: {{.GW}}}]
  hostnames: ["{{.Host}}"]
  rules:
  - filters:
    - type: URLRewrite
      urlRewrite: {hostname: rewritten.example.com}
    backendRefs:
    - {group: networking.istio.io, kind: Hostname, name: "{{.Name}}.{{.ExternalHost}}", port: 80}
`, map[string]any{"GW": gw.Name})
				gw.Expect(t, Request{}, check.And(
					check.RequestHeader("X-Forwarded-Port", "80"),
					check.RequestHeader("X-Forwarded-Host", s.Host())))
			},
		},
		Feature{
			ID:          "proxyconfig.proxyheaders.metadata-exchange-in-mesh",
			Description: "proxyHeaders.metadataExchangeHeaders mode IN_MESH keeps Istio peer-metadata headers off requests to out-of-mesh backends.",
			Roles:       ingressRole,
			Covers: []string{"ProxyConfig.proxyHeaders.metadataExchangeHeaders", "ProxyConfig.proxyHeaders.metadataExchangeHeaders.mode",
				"ProxyConfig.proxyHeaders.metadataExchangeHeaders.mode=IN_MESH"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := proxyConfigGateway(t, s, `{"proxyHeaders":{"metadataExchangeHeaders":{"mode":"IN_MESH"}}}`, true)
				gw.ExpectStable(t, Request{}, check.And(check.OK(), noRequestHeaderPrefix("X-Envoy-Peer-Metadata")))
			},
		},

		// ---- connectionSettings ----
		Feature{
			ID:          "proxyconfig.connection.headers-with-underscores",
			Description: "connectionSettings.httpHeadersWithUnderscoresAction REJECT_REQUEST rejects (400) and DROP_HEADER drops headers with underscores; ALLOW forwards them.",
			Roles:       ingressRole,
			Covers: []string{"ProxyConfig.connectionSettings", "ProxyConfig.connectionSettings.httpHeadersWithUnderscoresAction",
				"ProxyConfig.connectionSettings.httpHeadersWithUnderscoresAction=HEADERS_WITH_UNDERSCORES_REJECT_REQUEST",
				"ProxyConfig.connectionSettings.httpHeadersWithUnderscoresAction=HEADERS_WITH_UNDERSCORES_DROP_HEADER",
				"ProxyConfig.connectionSettings.httpHeadersWithUnderscoresAction=HEADERS_WITH_UNDERSCORES_ALLOW"},
			Run: func(t framework.TestContext, s *Scenario) {
				hdr := H("x_pcs", "v")
				rej := proxyConfigGateway(t, s, `{"connectionSettings":{"httpHeadersWithUnderscoresAction":"HEADERS_WITH_UNDERSCORES_REJECT_REQUEST"}}`, true, "rej")
				rej.Expect(t, Request{Headers: hdr}, check.Status(400))
				drop := proxyConfigGateway(t, s, `{"connectionSettings":{"httpHeadersWithUnderscoresAction":"HEADERS_WITH_UNDERSCORES_DROP_HEADER"}}`, true, "drop")
				drop.ExpectStable(t, Request{Headers: hdr}, RequestHeaderAbsent("x_pcs"))
				allow := proxyConfigGateway(t, s, `{"connectionSettings":{"httpHeadersWithUnderscoresAction":"HEADERS_WITH_UNDERSCORES_ALLOW"}}`, true, "allow")
				allow.Expect(t, Request{Headers: hdr}, check.RequestHeader("x_pcs", "v"))
			},
		},
		Feature{
			ID:          "proxyconfig.connection.escaped-slashes",
			Description: "connectionSettings.httpPathWithEscapedSlashesAction: REJECT_REQUEST answers 400, UNESCAPE_AND_REDIRECT redirects, UNESCAPE_AND_FORWARD and KEEP_UNCHANGED forward the decoded/original path.",
			Roles:       ingressRole,
			Covers: []string{"ProxyConfig.connectionSettings.httpPathWithEscapedSlashesAction",
				"ProxyConfig.connectionSettings.httpPathWithEscapedSlashesAction=REJECT_REQUEST",
				"ProxyConfig.connectionSettings.httpPathWithEscapedSlashesAction=UNESCAPE_AND_REDIRECT",
				"ProxyConfig.connectionSettings.httpPathWithEscapedSlashesAction=UNESCAPE_AND_FORWARD",
				"ProxyConfig.connectionSettings.httpPathWithEscapedSlashesAction=KEEP_UNCHANGED"},
			Run: func(t framework.TestContext, s *Scenario) {
				const p = "/a%2Fb"
				cfg := func(a string) string {
					return `{"connectionSettings":{"httpPathWithEscapedSlashesAction":"` + a + `"}}`
				}
				proxyConfigGateway(t, s, cfg("REJECT_REQUEST"), true, "rej").Expect(t, Request{Path: p}, check.Status(400))
				proxyConfigGateway(t, s, cfg("UNESCAPE_AND_REDIRECT"), true, "redir").Expect(t, Request{Path: p},
					check.And(statusIn(301, 302, 307, 308), ResponseHeaderContains("Location", "/a/b")))
				proxyConfigGateway(t, s, cfg("UNESCAPE_AND_FORWARD"), true, "fwd").Expect(t, Request{Path: p}, Path("/a/b"))
				proxyConfigGateway(t, s, cfg("KEEP_UNCHANGED"), true, "keep").Expect(t, Request{Path: p}, Path(p))
			},
		},
		Feature{
			ID:          "proxyconfig.connection.merge-slashes",
			Description: "connectionSettings.httpMergeSlashes merges repeated slashes before route matching.",
			Roles:       ingressRole,
			Covers:      []string{"ProxyConfig.connectionSettings.httpMergeSlashes"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := proxyConfigGateway(t, s, `{"connectionSettings":{"httpMergeSlashes":true}}`, false)
				s.Apply(t, `
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}-merge
spec:
  parentRefs: [{name: {{.GW}}}]
  hostnames: ["{{.Host}}"]
  rules:
  - matches: [{path: {type: Exact, value: /a/b}}]
    backendRefs: [{name: {{.BackendV2}}, port: 80}]
  - backendRefs: [{name: {{.BackendV1}}, port: 80}]
`, map[string]any{"GW": gw.Name})
				// A leading "//" would read as an authority in the request target; start with one slash.
				gw.Expect(t, Request{Path: "/a//b"}, Version("v2"))
			},
		},
	)
}

// proxyConfigGateway creates a dedicated gateway with the given ProxyConfig (JSON) in the
// proxy.istio.io/config annotation. If external, it routes Host() to the out-of-mesh backend;
// otherwise the caller adds routes.
func proxyConfigGateway(t framework.TestContext, s *Scenario, proxyConfig string, external bool, suffix ...string) *Gateway {
	sfx := ""
	if len(suffix) > 0 {
		sfx = suffix[0]
	}
	gw := s.NewGateway(t, GatewayOptions{Suffix: sfx, Annotations: map[string]string{"proxy.istio.io/config": proxyConfig}})
	if external {
		s.Apply(t, externalRoute(gw), map[string]any{"Name": s.Name(sfx)})
		gw.Expect(t, Request{}, FromService(ExternalSvc))
	}
	return gw
}

// statusIn checks the response status is one of codes.
func statusIn(codes ...int) echo.Checker {
	return check.Each(func(r echoClient.Response) error {
		for _, c := range codes {
			if r.Code == fmt.Sprint(c) {
				return nil
			}
		}
		return fmt.Errorf("status %s, want one of %v", r.Code, codes)
	})
}

// noRequestHeaderPrefix checks the backend saw no request header starting with prefix.
func noRequestHeaderPrefix(prefix string) echo.Checker {
	return check.Each(func(r echoClient.Response) error {
		for k := range r.RequestHeaders {
			if len(k) >= len(prefix) && http1Canon(k[:len(prefix)]) == http1Canon(prefix) {
				return fmt.Errorf("unexpected request header %q", k)
			}
		}
		return nil
	})
}

func http1Canon(s string) string { return textproto.CanonicalMIMEHeaderKey(s) }
