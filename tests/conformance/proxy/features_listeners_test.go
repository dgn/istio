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

// Listener, protocol and TLS-termination behaviors of the gateway.
func init() {
	Register(
		Feature{
			ID: "listener.tls.terminate-tcproute",
			Description: "A TLS listener in Terminate mode accepts a TCPRoute (Istio extension; Gateway API only defines TLSRoute here) " +
				"and forwards the decrypted stream.",
			Roles:  ingressRole,
			Covers: []string{"gateway-api-ext:tls-terminate-listener:TCPRoute"},
			Run: func(t framework.TestContext, s *Scenario) {
				host := s.Host()
				c := newCerts(t, host, host)
				s.TLSSecret(t, s.Name("cert"), c)
				gw := s.NewGateway(t, GatewayOptions{Listeners: `
  - name: tls
    port: 443
    protocol: TLS
    hostname: "{{.Host}}"
    tls:
      mode: Terminate
      certificateRefs: [{name: {{.Cert}}}]
    allowedRoutes:
      kinds: [{kind: TCPRoute}]
`, ListenerVars: map[string]any{"Cert": s.Name("cert")}})
				s.Apply(t, `
apiVersion: gateway.networking.k8s.io/v1
kind: TCPRoute
metadata:
  name: {{.Name}}
spec:
  parentRefs:
  - name: {{.GW}}
  rules:
  - backendRefs:
    - {name: {{.BackendV1}}, port: 80}
`, map[string]any{"GW": gw.Name})
				gw.Expect(t, Request{Port: 443, Scheme: scheme.HTTPS, Opts: tlsClient(host, c.CA)}, Version("v1"))
			},
		},
		Feature{
			ID:          "listener.tls-option.terminate-mode-mutual",
			Description: "Listener tls.options gateway.istio.io/tls-terminate-mode=MUTUAL requires a client certificate signed by the ca.crt in the certificate secret.",
			Roles:       ingressRole,
			Covers:      []string{"gateway-api-ext:tls-option:gateway.istio.io/tls-terminate-mode=MUTUAL"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw, server, client := mutualListener(t, s, "MUTUAL")
				host := s.Host()
				// The backend is out of mesh, so XFCC is exactly what the gateway set from the verified client cert.
				gw.Expect(t, Request{Port: 443, Scheme: scheme.HTTPS, Opts: func(o *echo.CallOptions) {
					tlsClient(host, server.CA)(o)
					o.TLS.Cert, o.TLS.Key = client.Cert, client.Key
				}}, check.And(check.OK(), RequestHeaderContains("X-Forwarded-Client-Cert", "CN=client")))
				gw.Expect(t, Request{Port: 443, Scheme: scheme.HTTPS, Opts: tlsClient(host, server.CA)}, check.Error())
			},
		},
		Feature{
			ID:          "listener.tls-option.terminate-mode-optional-mutual",
			Description: "Listener tls.options gateway.istio.io/tls-terminate-mode=OPTIONAL_MUTUAL accepts clients with or without a certificate.",
			Roles:       ingressRole,
			Covers:      []string{"gateway-api-ext:tls-option:gateway.istio.io/tls-terminate-mode=OPTIONAL_MUTUAL"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw, server, client := mutualListener(t, s, "OPTIONAL_MUTUAL")
				host := s.Host()
				// With a cert: verified and forwarded (proves the gateway requested it). Without: still accepted.
				gw.Expect(t, Request{Port: 443, Scheme: scheme.HTTPS, Opts: func(o *echo.CallOptions) {
					tlsClient(host, server.CA)(o)
					o.TLS.Cert, o.TLS.Key = client.Cert, client.Key
				}}, check.And(check.OK(), RequestHeaderContains("X-Forwarded-Client-Cert", "CN=client")))
				gw.Expect(t, Request{Port: 443, Scheme: scheme.HTTPS, Opts: tlsClient(host, server.CA)},
					check.And(check.OK(), headerNotContains("X-Forwarded-Client-Cert", "CN=client")))
			},
		},
		Feature{
			ID:          "protocol.websocket",
			Description: "WebSocket upgrades through an HTTP route are proxied to the backend.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				// echo's WebSocket client does not send a custom Host header, so use a dedicated
				// gateway with a route matching any host.
				gw := s.NewGateway(t, GatewayOptions{})
				s.Apply(t, `
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}
spec:
  parentRefs: [{name: {{.GW}}}]
  rules:
  - backendRefs: [{name: {{.BackendV1}}, port: 80}]
`, map[string]any{"GW": gw.Name})
				gw.Expect(t, Request{Scheme: scheme.WebSocket}, check.OK())
			},
		},
		Feature{
			ID:          "protocol.http2-downstream",
			Description: "The gateway accepts HTTP/2 cleartext (h2c, prior knowledge) from clients on an HTTP listener.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				s.Apply(t, httpRouteToShared(s, "{{.BackendV1}}", 80), s.sharedVars())
				s.SharedGateway().Expect(t, Request{HTTP2: true}, Version("v1"))
			},
		},
		Feature{
			ID:          "protocol.http2-upstream-appprotocol",
			Description: "A backend Service port with appProtocol/name 'http2' or 'grpc' is reached over HTTP/2.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				// The backend's "http2" port (85) is declared as HTTP/2 by name.
				s.Apply(t, httpRouteToShared(s, "{{.Backend}}", 85), s.sharedVars())
				s.SharedGateway().Expect(t, Request{}, check.And(check.OK(), check.Protocol("HTTP/2.0")))
			},
		},
		Feature{
			ID:          "protocol.http1-upstream-default",
			Description: "A backend Service port declared as HTTP is reached over HTTP/1.1 even when the client uses HTTP/2.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				s.Apply(t, httpRouteToShared(s, "{{.BackendV1}}", 80), s.sharedVars())
				s.SharedGateway().Expect(t, Request{HTTP2: true}, check.And(check.OK(), check.Protocol("HTTP/1.1")))
			},
		},
	)
}

// Istio-specific listener TLS options without a certificate secret, or restricting the handshake.
func init() {
	Register(
		Feature{
			ID: "listener.tls-option.terminate-mode-istio-mutual",
			Description: "Listener tls.options gateway.istio.io/tls-terminate-mode=ISTIO_MUTUAL terminates Istio mTLS from a sidecar " +
				"without a certificate secret, and refuses clients without a mesh certificate.",
			Roles:  ingressRole,
			Covers: []string{"gateway-api-ext:tls-option:gateway.istio.io/tls-terminate-mode=ISTIO_MUTUAL"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := istioModeListener(t, s, "ISTIO_MUTUAL")
				host := s.Host()
				vip := meshMTLSDestination(t, s, gw, "mtls", 443, host)
				s.Case(t, "mesh-client", func(t framework.TestContext) {
					gw.MeshExpect(t, Request{Address: vip, Port: 443}, Version("v1"))
				})
				s.Case(t, "no-mesh-cert", func(t framework.TestContext) {
					gw.ExpectStable(t, Request{Address: gw.PodIP(t), Port: 443, Scheme: scheme.HTTPS, Opts: insecureTLSClient(host, Certs{})}, check.Error())
				})
			},
		},
		Feature{
			ID: "listener.tls-option.terminate-mode-istio-simple",
			Description: "Listener tls.options gateway.istio.io/tls-terminate-mode=ISTIO_SIMPLE terminates TLS without a certificate secret, " +
				"serving the gateway's Istio workload certificate.",
			Roles:  ingressRole,
			Covers: []string{"gateway-api-ext:tls-option:gateway.istio.io/tls-terminate-mode=ISTIO_SIMPLE"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := istioModeListener(t, s, "ISTIO_SIMPLE")
				host, addr := s.Host(), gw.PodIP(t)
				gw.Expect(t, Request{Address: addr, Port: 443, Scheme: scheme.HTTPS, Opts: insecureTLSClient(host, Certs{})}, Version("v1"))
				s.expectHandshake(t, addr, 443, host, tlsProbe{
					name: "workload-certificate", args: "-showcerts", accepted: true,
					pipe: "openssl x509 -noout -ext subjectAltName", want: "URI:spiffe://[^/]+/ns/" + s.Namespace() + "/sa/",
				})
			},
		},
		Feature{
			ID: "listener.tls-option.auto-passthrough",
			Description: "Listener tls.options gateway.istio.io/listener-protocol=auto-passthrough on a Passthrough listener forwards Istio mTLS " +
				"by the outbound cluster in the SNI to services matching the listener hostname, without a route.",
			Roles:  ingressRole,
			Covers: []string{"gateway-api-ext:tls-option:gateway.istio.io/listener-protocol=auto-passthrough"},
			Run: func(t framework.TestContext, s *Scenario) {
				fqdn := func(svc string) string { return svc + "." + s.Namespace() + ".svc.cluster.local" }
				v1, v2 := fqdn(BackendSvc+"-v1"), fqdn(BackendSvc+"-v2")
				gw := s.NewGateway(t, GatewayOptions{Listeners: `
  - name: tls
    port: 443
    protocol: TLS
    hostname: "` + v1 + `"
    tls:
      mode: Passthrough
      options:
        gateway.istio.io/listener-protocol: auto-passthrough
`})
				sni := func(h string) string { return "outbound_.80_._." + h }
				vip1 := meshMTLSDestination(t, s, gw, "v1", 443, sni(v1))
				vip2 := meshMTLSDestination(t, s, gw, "v2", 443, sni(v2))
				s.Case(t, "mesh-client-listed-host", func(t framework.TestContext) {
					gw.MeshExpect(t, Request{Address: vip1, Port: 443}, Version("v1"))
				})
				s.Case(t, "mesh-client-other-host", func(t framework.TestContext) {
					gw.MeshExpectStable(t, Request{Address: vip2, Port: 443}, check.Error())
				})
				s.Case(t, "non-istio-client", func(t framework.TestContext) {
					gw.ExpectStable(t, Request{Address: gw.PodIP(t), Port: 443, Scheme: scheme.HTTPS, Opts: insecureTLSClient(sni(v1), Certs{})}, check.Error())
				})
			},
		},
		Feature{
			ID:          "listener.tls-option.cipher-suites",
			Description: "Listener tls.options gateway.istio.io/tls-cipher-suites restricts TLS 1.2 handshakes to the listed cipher suites.",
			Roles:       ingressRole,
			Covers:      []string{"gateway-api-ext:tls-option:gateway.istio.io/tls-cipher-suites"},
			Run: func(t framework.TestContext, s *Scenario) {
				host := s.Host()
				c := newCerts(t, host, host)
				s.TLSSecret(t, s.Name("cert"), c)
				gw := s.NewGateway(t, GatewayOptions{Listeners: `
  - name: https
    port: 443
    protocol: HTTPS
    hostname: "{{.Host}}"
    tls:
      mode: Terminate
      certificateRefs: [{name: {{.Cert}}}]
      options:
        gateway.istio.io/tls-cipher-suites: ECDHE-ECDSA-AES256-GCM-SHA384
`, ListenerVars: map[string]any{"Cert": s.Name("cert")}})
				s.Apply(t, httpRouteTo(gw, "{{.BackendV1}}", 80))
				addr := gw.PodIP(t)
				gw.Expect(t, Request{Address: addr, Port: 443, Scheme: scheme.HTTPS, Opts: tlsClient(host, c.CA)}, Version("v1"))
				for _, p := range []tlsProbe{
					{name: "listed-cipher", args: "-brief -tls1_2 -cipher ECDHE-ECDSA-AES256-GCM-SHA384", accepted: true, want: "Ciphersuite: ECDHE-ECDSA-AES256-GCM-SHA384"},
					{name: "other-cipher", args: "-brief -tls1_2 -cipher ECDHE-ECDSA-AES128-GCM-SHA256"},
				} {
					s.Case(t, p.name, func(t framework.TestContext) { s.expectHandshake(t, addr, 443, host, p) })
				}
			},
		},
	)
}

// istioModeListener creates a gateway with an HTTPS listener for the scenario Host using an Istio
// terminate mode that needs no certificate secret, routed to BackendV1.
func istioModeListener(t framework.TestContext, s *Scenario, mode string) *Gateway {
	gw := s.NewGateway(t, GatewayOptions{Listeners: `
  - name: https
    port: 443
    protocol: HTTPS
    hostname: "{{.Host}}"
    tls:
      mode: Terminate
      options:
        gateway.istio.io/tls-terminate-mode: {{.Mode}}
`, ListenerVars: map[string]any{"Mode": mode}})
	s.Apply(t, httpRouteTo(gw, "{{.BackendV1}}", 80))
	return gw
}

// httpRouteTo returns an HTTPRoute template attaching to gw for the scenario Host, routing to svc.
func httpRouteTo(gw *Gateway, svc string, port int) string {
	return `
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}
spec:
  parentRefs:
  - name: ` + gw.Name + `
  hostnames: ["{{.Host}}"]
  rules:
  - backendRefs:
    - name: ` + svc + `
      port: ` + itoa(port) + `
`
}

// httpRouteToShared is httpRouteTo for the shared gateway; apply it with s.sharedVars().
func httpRouteToShared(s *Scenario, svc string, port int) string {
	return httpRouteTo(s.shared, svc, port)
}

// tlsClient configures the call for TLS with the given SNI and trusted CA.
func tlsClient(sni, ca string) func(*echo.CallOptions) {
	return func(o *echo.CallOptions) {
		o.TLS.ServerName = sni
		o.TLS.CaCert = ca
	}
}

func newConnPerRequest(o *echo.CallOptions) { o.NewConnectionPerRequest = true }

// mutualListener creates a gateway with an HTTPS listener using the given Istio terminate mode. The
// server secret carries the client CA as ca.crt.
func mutualListener(t framework.TestContext, s *Scenario, mode string) (*Gateway, Certs, Certs) {
	host := s.Host()
	server := newCerts(t, host, host)
	client := newCerts(t, "client", "client.example.com")
	// Server cert/key, with the *client* CA as ca.crt.
	s.TLSSecret(t, s.Name("cert"), Certs{CA: client.CA, Cert: server.Cert, Key: server.Key})
	gw := s.NewGateway(t, GatewayOptions{Listeners: `
  - name: https
    port: 443
    protocol: HTTPS
    hostname: "{{.Host}}"
    tls:
      mode: Terminate
      certificateRefs: [{name: {{.Cert}}}]
      options:
        gateway.istio.io/tls-terminate-mode: {{.Mode}}
`, ListenerVars: map[string]any{"Cert": s.Name("cert"), "Mode": mode}})
	s.Apply(t, externalRoute(gw))
	return gw, server, client
}
