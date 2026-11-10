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
	"regexp"

	"istio.io/istio/pkg/slices"
	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
)

// Egress behaviors: how traffic from meshed workloads leaves the mesh through the egress gateway.
// The client is a sidecar workload; the proxy under test is the egress gateway the mesh routes to.
// Every feature routes its own external host, so egress features sharing the gateway cannot interfere.
func init() {
	Register(
		Feature{
			ID:          "egress.baseline.via-gateway",
			Description: "A meshed client reaches an external host through the egress gateway.",
			Roles:       []Role{RoleEgress},
			Covers: []string{
				"Gateway.selector", "Gateway.servers.hosts", "Gateway.servers.port",
				"Gateway.servers.port.number", "Gateway.servers.port.protocol",
				"VirtualService.hosts", "VirtualService.gateways", "VirtualService.http.match.gateways",
				"VirtualService.http.route.destination.host", "VirtualService.http.route.destination.port",
				"ServiceEntry.hosts", "ServiceEntry.location=MESH_EXTERNAL", "ServiceEntry.resolution=DNS",
				"ServiceEntry.endpoints.address",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				host := s.EgressHost()
				s.applyEgressRoute(t, host)
				// The gateway adds the header on its leg of the route. Seeing it at the external
				// backend proves the request went through the gateway instead of leaving the mesh
				// directly; without the VirtualService (negative control) it goes direct and the
				// header is absent.
				s.External(host).Expect(t, Request{Path: "/"},
					check.And(RequestHeaderContains("handled-by-egress-gateway", "true"), FromService(ExternalSvc)))
			},
		},
		Feature{
			ID:          "egress.authz.l7-path",
			Description: "An AuthorizationPolicy on the egress gateway rejects only the denied path.",
			Roles:       []Role{RoleEgress},
			Covers: []string{
				"AuthorizationPolicy.selector", "AuthorizationPolicy.selector.matchLabels",
				"AuthorizationPolicy.action=DENY",
				"AuthorizationPolicy.rules.to.operation.hosts", "AuthorizationPolicy.rules.to.operation.paths",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				host := s.EgressHost()
				s.applyEgressRoute(t, host)
				// The policy selects egress gateway pods, and workload selectors only match pods in
				// the policy's own namespace: it goes to the gateway's namespace.
				// Scoping the rule to this feature's host keeps it off other features' traffic.
				s.ApplyTo(t, s.egress.Namespace, `
apiVersion: security.istio.io/v1
kind: AuthorizationPolicy
metadata:
  name: {{.Name}}
spec:
  selector:
    matchLabels:
      istio: {{.EgressLabel}}
  action: DENY
  rules:
  - to:
    - operation:
        hosts: ["{{.EgressHost}}"]
        paths: ["/blocked"]
`, map[string]any{"EgressHost": host})
				w := s.External(host)
				w.Expect(t, Request{Path: "/ok"}, check.OK())
				w.Expect(t, Request{Path: "/blocked"}, check.Status(403))
			},
		},
		Feature{
			ID:          "egress.tls-origination.ca",
			Description: "The egress gateway originates TLS to the external server and verifies it against a CA served through credentialName.",
			Roles:       []Role{RoleEgress},
			Covers: []string{
				"DestinationRule.host", "DestinationRule.trafficPolicy.tls.mode",
				"DestinationRule.trafficPolicy.tls.credentialName",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				host := s.EgressHost()
				// The DestinationRule is applied in the feature's namespace, which is where the
				// Gateway lives: a gateway picks up DestinationRules from its Gateway's namespace.
				// Istio serves credentialName secrets from the proxy's namespace. The secret holds
				// only ca.crt: the proxy validates the external server against it instead of the
				// system roots, which do not include the test CA.
				ca := s.Name("ca")
				s.CAConfigSecretIn(t, s.egress.Namespace, ca, echoTestCA())
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.EgressHost}}"]
  location: MESH_EXTERNAL
  resolution: DNS
  ports:
  # HTTP to the gateway; targetPort 443 plus the DestinationRule below originate TLS outside.
  - {number: 80, name: http, protocol: HTTP, targetPort: 443}
  endpoints:
  - address: {{.External}}.{{.ExternalNamespace}}.svc.cluster.local
---
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}
spec:
  host: {{.EgressHost}}
  trafficPolicy:
    tls:
      mode: SIMPLE
      credentialName: {{.CA}}
      sni: {{.TLSHost}}
      subjectAltNames: ["{{.TLSHost}}"]
---
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: {{.Name}}
spec:
  selector:
    istio: {{.EgressLabel}}
  servers:
  - port:
      number: 80
      name: http
      protocol: HTTP
    hosts: ["{{.EgressHost}}"]
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.EgressHost}}"]
  gateways: ["{{.Name}}", mesh]
  http:
  - match:
    - gateways: [mesh]
      port: 80
    route:
    - destination:
        host: {{.EgressService}}
        port:
          number: 80
  - match:
    - gateways: ["{{.Name}}"]
      port: 80
    route:
    - destination:
        host: {{.EgressHost}}
        port:
          number: 80
`, map[string]any{"EgressHost": host, "CA": ca, "TLSHost": BackendTLSHost})
				// The SNI the external server received proves the gateway originated TLS with the
				// configured name; the client itself sent plain HTTP to the gateway.
				s.External(host).Expect(t, Request{Path: "/"},
					check.And(FromService(ExternalSvc), check.SNI(BackendTLSHost)))
			},
		},
		Feature{
			ID:          "egress.tls-origination.untrusted",
			Description: "TLS origination with a subjectAltNames that does not match the external certificate fails with 503 instead of connecting.",
			Roles:       []Role{RoleEgress},
			Covers:      []string{"DestinationRule.trafficPolicy.tls.subjectAltNames"},
			Run: func(t framework.TestContext, s *Scenario) {
				host := s.EgressHost()
				ca := s.Name("ca")
				s.CAConfigSecretIn(t, s.egress.Namespace, ca, echoTestCA())
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.EgressHost}}"]
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
  host: {{.EgressHost}}
  trafficPolicy:
    tls:
      mode: SIMPLE
      credentialName: {{.CA}}
      sni: {{.TLSHost}}
      subjectAltNames: ["wrong.{{.TLSHost}}"]
---
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: {{.Name}}
spec:
  selector:
    istio: {{.EgressLabel}}
  servers:
  - port:
      number: 80
      name: http
      protocol: HTTP
    hosts: ["{{.EgressHost}}"]
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.EgressHost}}"]
  gateways: ["{{.Name}}", mesh]
  http:
  - match:
    - gateways: [mesh]
      port: 80
    route:
    - destination:
        host: {{.EgressService}}
        port:
          number: 80
  - match:
    - gateways: ["{{.Name}}"]
      port: 80
    route:
    - destination:
        host: {{.EgressHost}}
        port:
          number: 80
`, map[string]any{"EgressHost": host, "CA": ca, "TLSHost": BackendTLSHost})
				// The gateway's TLS handshake fails, so the mesh client sees a 503, not the
				// external backend's response.
				s.External(host).ExpectStable(t, Request{Path: "/"}, check.Status(503))
			},
		},
		Feature{
			ID:          "egress.tcp.via-gateway",
			Description: "The egress gateway forwards raw TCP to an external service without protocol decoding.",
			Roles:       []Role{RoleEgress},
			Covers: []string{
				"Gateway.servers.port.protocol", "VirtualService.tcp.match.gateways",
				"ServiceEntry.location", "ServiceEntry.resolution",
				"ServiceEntry.ports.name", "ServiceEntry.ports.number", "ServiceEntry.ports.protocol",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				host := s.EgressHost()
				// The gateway Service exposes port 443 (targetPort 8443); a Gateway server declaring
				// that port is translated to the targetPort the proxy listens on. Port 80 is taken by
				// the HTTP features sharing this gateway.
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.EgressHost}}"]
  location: MESH_EXTERNAL
  resolution: DNS
  ports:
  - {number: 9090, name: tcp, protocol: TCP}
  endpoints:
  - address: {{.External}}.{{.ExternalNamespace}}.svc.cluster.local
---
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: {{.Name}}
spec:
  selector:
    istio: {{.EgressLabel}}
  servers:
  - port:
      number: 443
      name: tcp
      protocol: TCP
    hosts: ["{{.EgressHost}}"]
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.EgressHost}}"]
  gateways: ["{{.Name}}", mesh]
  tcp:
  - match:
    - gateways: [mesh]
      port: 9090
    route:
    - destination:
        host: {{.EgressService}}
        port:
          number: 443
  - match:
    - gateways: ["{{.Name}}"]
      port: 443
    route:
    - destination:
        host: {{.EgressHost}}
        port:
          number: 9090
`, map[string]any{"EgressHost": host})
				// A raw TCP exchange has no header to prove the path, so the proof is the peer the
				// external server saw: through the gateway it is the gateway pod, not the client.
				s.External(host).Expect(t, Request{Port: 9090, Scheme: scheme.TCP},
					check.And(check.OK(), fromEgressGateway(t, s.egress)))
			},
		},
	)
}

// fromEgressGateway requires the external server to have seen an egress gateway pod as the
// connection's peer, which is how a protocol that cannot carry a header proves its path. Without the
// route config the request goes direct and the server sees the client instead, so the negative
// control fails, as it must.
func fromEgressGateway(t framework.TestContext, gw *Gateway) echo.Checker {
	var gateways []string
	for _, p := range gw.Pods(t) {
		gateways = append(gateways, p.Status.PodIP)
	}
	return func(result echo.CallResult, err error) error {
		if err != nil {
			return err
		}
		for _, r := range result.Responses {
			peer, ok := serverPeer(r.RawContent)
			if !ok {
				return fmt.Errorf("the external server did not report the connection's peer: %s", r.RawContent)
			}
			if !slices.Contains(gateways, peer) {
				return fmt.Errorf("the external server saw %s as the connection's peer, not an egress gateway pod (%v): traffic did not pass through the egress gateway",
					peer, gateways)
			}
		}
		return nil
	}
}

// serverPeer extracts the peer address the echo server reported. The response parser cannot be used
// for this: the echo client writes its own "SourceIP=<client>" line ahead of the server's response,
// and the parser reads the "IP=" out of that line. The server's response fields arrive one body line
// each, so match the fully prefixed line.
var serverPeerRe = regexp.MustCompile(`(?m)^\[\d+ body\] IP=(\S+)$`)

func serverPeer(raw string) (string, bool) {
	m := serverPeerRe.FindStringSubmatch(raw)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// DeployEgressGateway deploys the egress gateway under test, shared by the features of a proxy run:
// an Envoy Deployment injected with Istio's gateway template behind a Service, selected by a unique
// istio label, like the istio-egressgateway of an IstioOperator install. The suite's Istio install
// has no egress gateway, so the run owns this one and removes it with its namespace.
func (s *Scenario) DeployEgressGateway(t framework.TestContext) *Gateway {
	t.Helper()
	name := s.Name("egress")
	// The Service ports mirror the istio-egressgateway chart: features route to port 80 (HTTP)
	// and 443 (TCP), which the gateway listens on as 8080 and 8443.
	s.Apply(t, `
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{.EgressName}}
---
apiVersion: v1
kind: Service
metadata:
  name: {{.EgressName}}
spec:
  selector:
    istio: {{.EgressName}}
  ports:
  - {name: http2, port: 80, targetPort: 8080}
  - {name: https, port: 443, targetPort: 8443}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{.EgressName}}
spec:
  selector:
    matchLabels:
      istio: {{.EgressName}}
  template:
    metadata:
      annotations:
        inject.istio.io/templates: gateway
      labels:
        istio: {{.EgressName}}
        sidecar.istio.io/inject: "true"
    spec:
      serviceAccountName: {{.EgressName}}
      containers:
      - name: istio-proxy
        image: auto
`, map[string]any{"EgressName": name})
	gw := &Gateway{Name: name, Namespace: s.Namespace(), s: s, selector: "istio=" + name, container: "istio-proxy"}
	gw.waitReady(t)
	return gw
}

// EgressHost returns the external hostname this feature routes through the egress gateway. It is
// unique per feature, so egress features can share the gateway.
func (s *Scenario) EgressHost() string {
	return s.Name() + "." + ExternalHost
}

// applyEgressRoute deploys the ServiceEntry, Istio Gateway and VirtualService that route host through
// the egress gateway: from the mesh to the gateway Service, and from the gateway out to the host.
func (s *Scenario) applyEgressRoute(t framework.TestContext, host string) {
	t.Helper()
	extra := map[string]any{"EgressHost": host}
	s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.EgressHost}}"]
  location: MESH_EXTERNAL
  resolution: DNS
  ports:
  - number: 80
    name: http
    protocol: HTTP
  endpoints:
  - address: {{.External}}.{{.ExternalNamespace}}.svc.cluster.local
---
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: {{.Name}}
spec:
  selector:
    istio: {{.EgressLabel}}
  servers:
  - port:
      number: 80
      name: http
      protocol: HTTP
    hosts: ["{{.EgressHost}}"]
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.EgressHost}}"]
  gateways: ["{{.Name}}", mesh]
  http:
  - match:
    - gateways: [mesh]
      port: 80
    route:
    - destination:
        host: {{.EgressService}}
        port:
          number: 80
  - match:
    - gateways: ["{{.Name}}"]
      port: 80
    route:
    - destination:
        host: {{.EgressHost}}
    headers:
      request:
        add:
          handled-by-egress-gateway: "true"
`, extra)
}
