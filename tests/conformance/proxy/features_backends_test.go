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
	"strings"

	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
)

// Backend behaviors: how the gateway reaches what it routes to (mesh mTLS, ServiceEntry,
// DestinationRule, BackendTLSPolicy).
func init() {
	Register(
		// --- ServiceEntry towards meshed and synthetic destinations ---
		Feature{
			ID:          "backend.serviceentry.addresses",
			Description: "A client calling a ServiceEntry address (no DNS for the host) reaches the ServiceEntry's STATIC endpoints.",
			Roles:       []Role{RoleSidecar, RoleWaypoint},
			RequiresL7:  true,
			Covers:      []string{"ServiceEntry.addresses", "ServiceEntry.resolution=STATIC", "ServiceEntry.endpoints.ports"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone})
				tg.ServiceEntry(t, `
  location: MESH_EXTERNAL
  resolution: STATIC
  ports:
  - {number: 80, name: http, protocol: HTTP}
  endpoints:
  - {address: {{.IP}}, ports: {http: 18080}}
`, map[string]any{"IP": s.Env.External[0].WorkloadsOrFail(t)[0].Address()})
				tg.Expect(t, Request{}, FromService(ExternalSvc))
			},
		},
		Feature{
			ID:          "backend.serviceentry.workload-selector",
			Description: "A MESH_INTERNAL ServiceEntry with a workloadSelector uses the selected pods as its endpoints.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"ServiceEntry.workloadSelector", "ServiceEntry.workloadSelector.labels", "ServiceEntry.location=MESH_INTERNAL",
				"interaction:serviceentry.workload-selector",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone})
				// Only v2 is selected; the backend's default host serves v1 only.
				tg.ServiceEntry(t, `
  location: MESH_INTERNAL
  resolution: STATIC
  workloadSelector:
    labels: {app: {{.DestApp}}, version: v2}
  ports:
  - {number: 80, name: http, protocol: HTTP, targetPort: 18080}
`)
				tg.Expect(t, Request{Count: 5}, Version("v2"))
			},
		},
		Feature{
			ID:          "backend.mesh.auto-mtls",
			Description: "Traffic from the gateway to a sidecar backend uses Istio mTLS; the backend sees the gateway's SPIFFE identity.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				s.Apply(t, httpRouteToShared(s, "{{.BackendV1}}", 80), s.sharedVars())
				s.SharedGateway().Expect(t, Request{}, check.And(
					check.OK(),
					RequestHeaderContains("X-Forwarded-Client-Cert", "URI=spiffe://cluster.local/ns/"+s.Namespace()+"/sa/"),
				))
			},
		},
		Feature{
			ID:          "backend.mesh.peerauthentication-strict",
			Description: "The gateway reaches a backend whose port requires STRICT mTLS (PeerAuthentication).",
			Roles:       ingressRole,
			Covers: []string{
				"PeerAuthentication.selector", "PeerAuthentication.mtls", "PeerAuthentication.mtls.mode=PERMISSIVE", "PeerAuthentication.portLevelMtls",
				"PeerAuthentication.portLevelMtls.mode", "PeerAuthentication.portLevelMtls.mode=STRICT",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				// STRICT only on the backend's http2 port (workload port 18085), so other features
				// using port 80 are unaffected.
				s.Apply(t, `
apiVersion: security.istio.io/v1
kind: PeerAuthentication
metadata:
  name: {{.Name}}
spec:
  selector:
    matchLabels:
      app: {{.Backend}}
  mtls:
    mode: PERMISSIVE
  portLevelMtls:
    18085:
      mode: STRICT
`)
				s.Apply(t, httpRouteToShared(s, "{{.Backend}}", 85), s.sharedVars())
				// STRICT is in effect: a plaintext (out-of-mesh) client is refused on that port...
				s.Env.Client.CallOrFail(t, echo.CallOptions{To: s.Env.Backend, Port: echo.Port{Name: "http2"}, Check: check.Error()})
				// ...while the gateway still gets through.
				s.SharedGateway().ExpectStable(t, Request{}, check.OK())
			},
		},
		Feature{
			ID:          "backend.mesh.authz-gateway-principal",
			Description: "A backend AuthorizationPolicy allowing only the gateway's principal admits traffic from the gateway.",
			Roles:       ingressRole,
			Covers: []string{
				"AuthorizationPolicy.rules.from.source.principals", "AuthorizationPolicy.rules.to.operation.ports",
				"AuthorizationPolicy.rules.to.operation.notPorts",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				// Allow-only-the-gateway on the reserved http-authz port (18088), so other features are unaffected.
				gw := s.NewGateway(t, GatewayOptions{})
				s.Apply(t, httpRouteTo(gw, "{{.Backend}}", 88))
				// The gateway's service account is not standardized across implementations; read it
				// from the gateway pod.
				sa := gw.ServiceAccount(t)
				s.Apply(t, `
apiVersion: security.istio.io/v1
kind: AuthorizationPolicy
metadata:
  name: {{.Name}}
spec:
  selector:
    matchLabels:
      app: {{.Backend}}
  action: ALLOW
  rules:
  - from:
    - source:
        principals: ["cluster.local/ns/{{.Namespace}}/sa/{{.SA}}"]
    to:
    - operation:
        ports: ["18088"]
  - to:
    - operation:
        notPorts: ["18088"]
`, map[string]any{"SA": sa})
				// Another caller (the out-of-mesh client, no principal) is denied on that port...
				s.Env.Client.CallOrFail(t, echo.CallOptions{To: s.Env.Backend, Port: echo.Port{Name: "http-authz"}, Check: check.Status(403)})
				// ...while the gateway's principal is allowed.
				gw.ExpectStable(t, Request{}, check.OK())
			},
		},
		Feature{
			ID:          "backend.serviceentry.hostname-backendref",
			Description: "An HTTPRoute backendRef of kind Hostname (networking.istio.io) routes to a ServiceEntry host.",
			Roles:       ingressRole,
			Covers: []string{
				"gateway-api-ext:backendref-kind:networking.istio.io/Hostname", "ServiceEntry.hosts", "ServiceEntry.resolution=DNS", "ServiceEntry.endpoints",
				"ServiceEntry.endpoints.address", "ServiceEntry.ports", "ServiceEntry.location=MESH_EXTERNAL",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				s.Apply(t, externalServiceEntry())
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
  - backendRefs:
    - {group: networking.istio.io, kind: Hostname, name: {{.SEHost}}, port: 80}
`, s.sharedVars(), map[string]any{"SEHost": s.Name() + "." + ExternalHost})
				s.SharedGateway().Expect(t, Request{}, FromService(ExternalSvc))
			},
		},
		Feature{
			ID:          "backend.serviceentry.static-endpoints",
			Description: "A ServiceEntry with STATIC resolution and explicit endpoint IPs is routable through the gateway.",
			Roles:       ingressRole,
			Covers:      []string{"ServiceEntry.resolution=STATIC", "ServiceEntry.ports.targetPort"},
			Run: func(t framework.TestContext, s *Scenario) {
				ip := s.Env.External[0].WorkloadsOrFail(t)[0].Address()
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
  - {number: 80, name: http, protocol: HTTP, targetPort: 18080}
  endpoints:
  - address: {{.IP}}
`, map[string]any{"SEHost": s.Name() + "." + ExternalHost, "IP": ip})
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
  - backendRefs:
    - {group: networking.istio.io, kind: Hostname, name: {{.SEHost}}, port: 80}
`, s.sharedVars(), map[string]any{"SEHost": s.Name() + "." + ExternalHost})
				s.SharedGateway().Expect(t, Request{}, FromService(ExternalSvc))
			},
		},
		Feature{
			ID:          "backend.destinationrule.tls-origination",
			Description: "A DestinationRule with tls.mode SIMPLE originates TLS from the gateway to an HTTPS backend.",
			Roles:       ingressRole,
			Covers: []string{
				"DestinationRule.host", "DestinationRule.trafficPolicy.tls", "DestinationRule.trafficPolicy.tls.mode=SIMPLE",
				"DestinationRule.trafficPolicy.tls.credentialName", "DestinationRule.trafficPolicy.tls.sni",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				// ServiceEntry port 80 (HTTP) → external echo's TLS port; the DR upgrades to TLS.
				host := s.Name() + "." + ExternalHost
				s.CAConfigSecret(t, s.Name("ca"), echoTestCA())
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
  - {number: 80, name: http, protocol: HTTP, targetPort: 443}
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
    tls:
      mode: SIMPLE
      credentialName: {{.CA}}
      sni: {{.TLSHost}}
      subjectAltNames: ["{{.TLSHost}}"]
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
`, s.sharedVars(), map[string]any{"SEHost": host, "CA": s.Name("ca"), "TLSHost": BackendTLSHost})
				s.SharedGateway().Expect(t, Request{}, check.And(FromService(ExternalSvc), check.SNI(BackendTLSHost)))
			},
		},
		Feature{
			ID:          "backend.destinationrule.tls-san-mismatch",
			Description: "TLS origination with a non-matching subjectAltNames fails (503) instead of connecting.",
			Roles:       ingressRole,
			Covers:      []string{"DestinationRule.trafficPolicy.tls.subjectAltNames"},
			Run: func(t framework.TestContext, s *Scenario) {
				host := s.Name() + "." + ExternalHost
				s.CAConfigSecret(t, s.Name("ca"), echoTestCA())
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
  - {number: 80, name: http, protocol: HTTP, targetPort: 443}
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
    tls:
      mode: SIMPLE
      credentialName: {{.CA}}
      sni: {{.TLSHost}}
      subjectAltNames: ["wrong.example.com"]
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
`, s.sharedVars(), map[string]any{"SEHost": host, "CA": s.Name("ca"), "TLSHost": BackendTLSHost})
				s.SharedGateway().ExpectStable(t, Request{}, check.Status(503))
			},
		},
		Feature{
			ID:          "backend.destinationrule.consistent-hash",
			Description: "A DestinationRule consistentHash on a header pins requests with the same header value to one endpoint.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"DestinationRule.host", "DestinationRule.trafficPolicy.loadBalancer.consistentHash",
				"DestinationRule.trafficPolicy.loadBalancer.consistentHash.httpHeaderName", "ServiceEntry.workloadSelector",
				"ServiceEntry.location=MESH_INTERNAL",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				// The DestinationRule applies to a destination of this feature only (see DestinationPolicy).
				tg := s.Target(t, TargetOptions{Routing: RouteNone, Shared: true})
				tg.DestinationPolicy(t, `
    loadBalancer:
      consistentHash:
        httpHeaderName: x-user
`)
				tg.Expect(t, Request{Count: 20, Headers: H("x-user", "alice")}, check.And(check.OK(), sameHostname()))
			},
		},
		Feature{
			ID:          "backend.mesh.peerauthentication-disable",
			Description: "The gateway reaches a backend port whose PeerAuthentication is DISABLE (plaintext only), sending plaintext.",
			Roles:       ingressRole,
			Covers:      []string{"PeerAuthentication.portLevelMtls.mode", "PeerAuthentication.portLevelMtls.mode=DISABLE"},
			Run: func(t framework.TestContext, s *Scenario) {
				// Only workload port 18081 (service port 81) is reserved for this.
				s.Apply(t, `
apiVersion: security.istio.io/v1
kind: PeerAuthentication
metadata:
  name: {{.Name}}
spec:
  selector:
    matchLabels:
      app: {{.Backend}}
  mtls:
    mode: PERMISSIVE
  portLevelMtls:
    18081:
      mode: DISABLE
`)
				s.Apply(t, httpRouteToShared(s, "{{.Backend}}", 81), s.sharedVars())
				s.SharedGateway().ExpectStable(t, Request{}, check.And(check.OK(), RequestHeaderAbsent("X-Forwarded-Client-Cert")))
			},
		},
		Feature{
			ID:          "backend.serviceentry.dns-round-robin",
			Description: "A ServiceEntry with DNS_ROUND_ROBIN resolution is routable through the gateway.",
			Roles:       ingressRole,
			Covers:      []string{"ServiceEntry.resolution=DNS_ROUND_ROBIN"},
			Run: func(t framework.TestContext, s *Scenario) {
				s.Apply(t, strings.Replace(externalServiceEntry(), "resolution: DNS", "resolution: DNS_ROUND_ROBIN", 1))
				s.Apply(t, `
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}
spec:
  parentRefs: [{name: {{.SharedGateway}}}]
  hostnames: ["{{.Host}}"]
  rules:
  - backendRefs:
    - {group: networking.istio.io, kind: Hostname, name: {{.SEHost}}, port: 80}
`, s.sharedVars(), map[string]any{"SEHost": s.Name() + "." + ExternalHost})
				s.SharedGateway().Expect(t, Request{}, FromService(ExternalSvc))
			},
		},
	)
}

// externalServiceEntry returns a ServiceEntry template for a per-feature host resolving (DNS) to
// the external echo on HTTP port 80.
func externalServiceEntry() string {
	return `
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.Name}}.{{.ExternalHost}}"]
  location: MESH_EXTERNAL
  resolution: DNS
  ports:
  - {number: 80, name: port, protocol: HTTP}
  endpoints:
  - address: {{.External}}.{{.ExternalNamespace}}.svc.cluster.local
`
}

// sameHostname checks all responses came from one pod.
func sameHostname() echo.Checker {
	return func(result echo.CallResult, _ error) error {
		hosts := map[string]int{}
		for _, r := range result.Responses {
			hosts[r.Hostname]++
		}
		if len(hosts) != 1 {
			return fmt.Errorf("expected all responses from one endpoint, got %v", hosts)
		}
		return nil
	}
}

// externalRoute returns config routing Host() on gw to the out-of-mesh backend via a ServiceEntry.
// Use it when the backend must see the request exactly as the gateway sent it: an in-mesh backend's
// sidecar rewrites some headers (e.g. it replaces X-Forwarded-Client-Cert).
func externalRoute(gw *Gateway) string {
	return externalServiceEntry() + `---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}
spec:
  parentRefs: [{name: ` + gw.Name + `}]
  hostnames: ["{{.Host}}"]
  rules:
  - backendRefs:
    - {group: networking.istio.io, kind: Hostname, name: "{{.Name}}.{{.ExternalHost}}", port: 80}
`
}
