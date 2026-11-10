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

	echoClient "istio.io/istio/pkg/test/echo"
	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
)

// The Sidecar resource. Client-side settings (egress, outboundTrafficPolicy) select the feature's own
// client (TargetOptions.OwnClient), server-side settings (ingress, inboundConnectionPool) select the
// feature's backend, so neither affects other features.
func init() {
	Register(
		Feature{
			ID:          "sidecar.outbound-registry-only",
			Description: "A Sidecar with outboundTrafficPolicy REGISTRY_ONLY blocks traffic to addresses no service declares, while registered services stay reachable.",
			Roles:       []Role{RoleSidecar},
			Covers: []string{
				"Sidecar.workloadSelector", "Sidecar.workloadSelector.labels", "Sidecar.outboundTrafficPolicy", "Sidecar.outboundTrafficPolicy.mode",
				"Sidecar.outboundTrafficPolicy.mode=REGISTRY_ONLY", "interaction:outbound-traffic-policy.registry-only",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{OwnClient: true})
				ip := s.Env.External[0].WorkloadsOrFail(t)[0].Address()
				unregistered := Request{Address: ip, Port: 18080, Host: ip}
				tg.Expect(t, unregistered, FromService(ExternalSvc))
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: Sidecar
metadata:
  name: {{.Name}}
spec:
  workloadSelector:
    labels:
      app: {{.ClientApp}}
  outboundTrafficPolicy:
    mode: REGISTRY_ONLY
`)
				tg.Expect(t, unregistered, blocked())
				tg.ExpectStable(t, Request{}, check.OK())
			},
		},
		Feature{
			ID: "sidecar.egress-hosts",
			Description: "A Sidecar egress listener imports only the listed hosts: with REGISTRY_ONLY, " +
				"a service that is not imported is blocked while the imported one stays reachable.",
			Roles: []Role{RoleSidecar},
			Covers: []string{
				"Sidecar.egress", "Sidecar.egress.hosts", "Sidecar.egress.port", "Sidecar.egress.port.number", "Sidecar.egress.port.protocol",
				"Sidecar.egress.port.name", "interaction:visibility.sidecar-egress-hosts",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{OwnClient: true})
				tg.Expect(t, Request{Host: s.backend.V2, Address: s.backend.V2}, Version("v2"))
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: Sidecar
metadata:
  name: {{.Name}}
spec:
  workloadSelector:
    labels:
      app: {{.ClientApp}}
  egress:
  - port: {number: 80, protocol: HTTP, name: http}
    hosts: ["./{{.TargetHost}}.{{.Namespace}}.svc.cluster.local"]
  outboundTrafficPolicy:
    mode: REGISTRY_ONLY
`)
				tg.Expect(t, Request{Host: s.backend.V2, Address: s.backend.V2}, blocked())
				tg.ExpectStable(t, Request{}, Version("v1"))
			},
		},
		Feature{
			ID:          "sidecar.ingress-default-endpoint",
			Description: "A Sidecar ingress listener forwards inbound traffic on its port to the configured defaultEndpoint of the application.",
			Roles:       []Role{RoleSidecar},
			RequiresL7:  true,
			Covers: []string{
				"Sidecar.ingress", "Sidecar.ingress.port", "Sidecar.ingress.port.number", "Sidecar.ingress.port.protocol", "Sidecar.ingress.port.name",
				"Sidecar.ingress.defaultEndpoint",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				// The backend listens on 18080 (the Service's target port) and 18083 (no Service port).
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: Sidecar
metadata:
  name: {{.Name}}
spec:
  workloadSelector:
    labels:
      app: {{.DestApp}}
  ingress:
  - port: {number: 18080, protocol: HTTP, name: http}
    defaultEndpoint: 127.0.0.1:18083
`)
				tg.Expect(t, Request{}, servedOnPort("18083"))
			},
		},
		Feature{
			ID: "sidecar.ingress-tls",
			Description: "A Sidecar ingress listener with tls (SIMPLE, serverCertificate and privateKey from mounted files) terminates TLS from clients " +
				"at the backend's sidecar and forwards plaintext to the application.",
			Roles: []Role{RoleSidecar},
			Covers: []string{
				"flag:istiod:ENABLE_TLS_ON_SIDECAR_INGRESS", "Sidecar.ingress.tls", "Sidecar.ingress.tls.mode", "Sidecar.ingress.tls.mode=SIMPLE",
				"Sidecar.ingress.tls.serverCertificate", "Sidecar.ingress.tls.privateKey",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				host := s.Host()
				c := newCerts(t, host, host)
				s.TLSSecret(t, s.Name("cred"), c)
				// Sidecar ingress TLS reads files (credentialName is not supported there): mount the secret into the sidecar.
				s.backendAnnotations = map[string]string{
					"sidecar.istio.io/userVolume":      `[{"name":"pcs-tls","secret":{"secretName":"` + s.Name("cred") + `"}}]`,
					"sidecar.istio.io/userVolumeMount": `[{"name":"pcs-tls","mountPath":"/etc/pcs-tls","readOnly":true}]`,
				}
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: Sidecar
metadata:
  name: {{.Name}}
spec:
  workloadSelector:
    labels:
      app: {{.DestApp}}
  ingress:
  - port: {number: 18080, protocol: HTTPS, name: https}
    defaultEndpoint: 127.0.0.1:18080
    tls:
      mode: SIMPLE
      serverCertificate: /etc/pcs-tls/tls.crt
      privateKey: /etc/pcs-tls/tls.key
---
# Sidecar ingress TLS is for clients outside the mesh: Istio applies it only where mTLS is disabled.
apiVersion: security.istio.io/v1
kind: PeerAuthentication
metadata:
  name: {{.Name}}
spec:
  selector:
    matchLabels:
      app: {{.DestApp}}
  portLevelMtls:
    18080:
      mode: DISABLE
`)
				// Dial the pod directly: the client's sidecar passes the TLS stream through to the server sidecar.
				addr := tg.BackendInstances()[0].WorkloadsOrFail(t)[0].Address()
				tg.Expect(t, Request{Address: addr, Host: host, Port: 18080, Scheme: scheme.HTTPS, Opts: tlsClient(host, c.CA)}, check.OK())
				tg.ExpectStable(t, Request{Address: addr, Host: host, Port: 18080}, check.Error())
			},
		},
		Feature{
			ID: "sidecar.inbound-connection-pool",
			Description: "A Sidecar inboundConnectionPool http2MaxRequests limits concurrent requests the backend's sidecar forwards to the application; " +
				"excess requests fail with 503.",
			Roles:      []Role{RoleSidecar},
			RequiresL7: true,
			Covers:     []string{"Sidecar.inboundConnectionPool", "Sidecar.inboundConnectionPool.http", "Sidecar.inboundConnectionPool.http.http2MaxRequests"},
			Run: sidecarInboundOverflow(`
  inboundConnectionPool:
    http: {http2MaxRequests: 1}
`),
		},
		Feature{
			ID: "sidecar.ingress-connection-pool",
			Description: "A Sidecar ingress listener connectionPool (tcp.maxConnections, http.http1MaxPendingRequests) " +
				"limits concurrent inbound requests on that port; excess requests fail with 503.",
			Roles:      []Role{RoleSidecar},
			RequiresL7: true,
			Covers: []string{
				"Sidecar.ingress.connectionPool", "Sidecar.ingress.connectionPool.tcp", "Sidecar.ingress.connectionPool.tcp.maxConnections",
				"Sidecar.ingress.connectionPool.http", "Sidecar.ingress.connectionPool.http.http1MaxPendingRequests",
			},
			Run: sidecarInboundOverflow(`
  ingress:
  - port: {number: 18080, protocol: HTTP, name: http}
    defaultEndpoint: 0.0.0.0:18080
    connectionPool:
      tcp: {maxConnections: 1}
      http: {http1MaxPendingRequests: 1}
`),
		},
	)
}

// sidecarInboundOverflow is the body of the inbound connection pool family: a Sidecar for the
// backend with spec (indented 2) must make 20 concurrent slow requests partly overflow with 503.
func sidecarInboundOverflow(spec string) func(t framework.TestContext, s *Scenario) {
	return func(t framework.TestContext, s *Scenario) {
		tg := s.Target(t, TargetOptions{})
		s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: Sidecar
metadata:
  name: {{.Name}}
spec:
  workloadSelector:
    labels:
      app: {{.DestApp}}
`+spec)
		tg.Expect(t, Request{Path: "/?delay=1s", Count: 20}, check.And(someStatus(200), someStatus(503)))
	}
}

// blocked checks that a request did not reach a backend: the proxy refused it (502 from the
// BlackHole route) or reset the connection.
func blocked() echo.Checker {
	return func(result echo.CallResult, err error) error {
		if err != nil || len(result.Responses) == 0 {
			return nil
		}
		for _, r := range result.Responses {
			if r.Code == "200" || r.Hostname != "" {
				return fmt.Errorf("expected the request to be blocked, got %s from %q", r.Code, r.Hostname)
			}
		}
		return nil
	}
}

// servedOnPort checks that the application received the requests on the given container port.
func servedOnPort(port string) echo.Checker {
	return check.And(check.OK(), check.Each(func(r echoClient.Response) error {
		if r.Port != port {
			return fmt.Errorf("expected the application to serve on port %s, got %s", port, r.Port)
		}
		return nil
	}))
}
