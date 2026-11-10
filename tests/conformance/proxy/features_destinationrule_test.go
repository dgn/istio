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
	"path"
	"strings"

	"istio.io/istio/pkg/test/env"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
	"istio.io/istio/pkg/test/util/file"
)

// DestinationRule traffic policies, as families: one body per family (a generator returning the
// feature's Run) and one Feature literal per value, so that each value has its own outcome and Covers.
// Every feature runs on each L7 role through a Target (DestinationPolicy for the feature's backend, or
// ServiceEntry for a per-feature external or synthetic destination).
func init() {
	Register(
		// --- connectionPool: limits are observable as 503 overflow under concurrent requests. ---
		Feature{
			ID:          "backend.destinationrule.connection-pool.pending-overflow",
			Description: "DestinationRule connectionPool tcp.maxConnections and http.http1MaxPendingRequests limit concurrent requests; excess requests fail with 503.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"DestinationRule.trafficPolicy", "DestinationRule.trafficPolicy.connectionPool", "DestinationRule.trafficPolicy.connectionPool.tcp",
				"DestinationRule.trafficPolicy.connectionPool.tcp.maxConnections", "DestinationRule.trafficPolicy.connectionPool.http",
				"DestinationRule.trafficPolicy.connectionPool.http.http1MaxPendingRequests",
			},
			Run: connectionPoolOverflow(`
    connectionPool:
      tcp: {maxConnections: 1}
      http: {http1MaxPendingRequests: 1}
`),
		},
		Feature{
			ID:          "backend.destinationrule.connection-pool.max-requests",
			Description: "DestinationRule connectionPool http.http2MaxRequests limits concurrent requests to the destination; excess requests fail with 503.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"DestinationRule.trafficPolicy.connectionPool.http.http2MaxRequests"},
			Run: connectionPoolOverflow(`
    connectionPool:
      http: {http2MaxRequests: 1}
`),
		},
		Feature{
			ID: "backend.destinationrule.connection-pool.max-concurrent-streams",
			Description: "DestinationRule connectionPool http.maxConcurrentStreams limits the streams of an upstream HTTP/2 connection: " +
				"with few connections allowed, excess concurrent requests fail with 503, and all succeed once the limit is removed.",
			Roles:      l7Roles,
			RequiresL7: true,
			Covers:     []string{"DestinationRule.trafficPolicy.connectionPool.http.maxConcurrentStreams"},
			Run: func(t framework.TestContext, s *Scenario) {
				// Envoy keeps one connection pool per worker thread, so maxConnections must cover the
				// worker count (proxy concurrency defaults to the CPU limit, 2) while staying well below
				// the 20 parallel requests so that phase 1 overflows.
				policy := func(streams string) string {
					return `
    connectionPool:
      tcp: {maxConnections: 4}
      http: {h2UpgradePolicy: UPGRADE, http1MaxPendingRequests: 1` + streams + `}
`
				}
				tg := s.Target(t, TargetOptions{Routing: RouteNone, Shared: true})
				if s.Proxy.Role == RoleSidecar {
					// The backend's sidecar applies the DestinationRule connectionPool to its HTTP/1
					// inbound cluster too, unless the Sidecar sets inboundConnectionPool. Lift that limit
					// so that only the client proxy's upstream connections are under test.
					s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: Sidecar
metadata:
  name: {{.Name}}
spec:
  workloadSelector:
    labels:
      app: {{.DestApp}}
  inboundConnectionPool:
    tcp: {maxConnections: 1024}
    http: {http1MaxPendingRequests: 1024, http2MaxRequests: 1024}
`)
				}
				tg.DestinationPolicy(t, policy(", maxConcurrentStreams: 1"))
				req := Request{Path: "/?delay=1s", Count: 20}
				tg.Expect(t, req, check.And(someStatus(200), someStatus(503)))
				// Without the stream limit all requests multiplex over the HTTP/2 connections. A proxy that
				// overflowed only because of maxConnections (e.g. it does not upgrade) still fails here.
				tg.DestinationPolicy(t, policy(""))
				tg.ExpectStable(t, req, check.OK())
			},
		},

		// --- outlierDetection ---
		Feature{
			ID:          "backend.destinationrule.outlier-detection",
			Description: "DestinationRule outlierDetection ejects an endpoint that answers with 5xx, so subsequent requests only reach healthy endpoints.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"DestinationRule.trafficPolicy.outlierDetection", "DestinationRule.trafficPolicy.outlierDetection.consecutive5xxErrors",
				"DestinationRule.trafficPolicy.outlierDetection.interval", "DestinationRule.trafficPolicy.outlierDetection.baseEjectionTime",
				"DestinationRule.trafficPolicy.outlierDetection.maxEjectionPercent", "ServiceEntry.endpoints.ports",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone, Shared: true})
				// Two endpoints: the external echo's HTTP port, and the TCP echo port of another pod,
				// which answers an HTTP request with garbage (an upstream protocol error, a 5xx that the
				// default retry policy does not retry). Endpoints sharing an IP would be deduplicated.
				host := tg.ServiceEntry(t, `
  location: MESH_EXTERNAL
  resolution: STATIC
  ports:
  - {number: 80, name: http, protocol: HTTP}
  endpoints:
  - {address: {{.GoodIP}}, ports: {http: 18080}}
  - {address: {{.BadIP}}, ports: {http: 19090}}
`, map[string]any{"GoodIP": s.Env.External[0].WorkloadsOrFail(t)[0].Address(), "BadIP": s.Env.Backend[0].WorkloadsOrFail(t)[0].Address()})
				// Both endpoints are in rotation before the DestinationRule exists.
				tg.Expect(t, Request{Count: 10, Opts: newConnPerRequest}, check.And(someStatus(200), some5xx()))
				s.Apply(t, destinationRule(`
    outlierDetection:
      consecutive5xxErrors: 1
      interval: 1s
      baseEjectionTime: 10m
      maxEjectionPercent: 100
`), map[string]any{"PolicyHost": host})
				tg.Expect(t, Request{Count: 10, Opts: newConnPerRequest}, check.And(check.OK(), FromService(ExternalSvc)))
			},
		},

		// --- tls: origination modes towards an out-of-mesh backend, and DISABLE towards a meshed one. ---
		Feature{
			ID: "backend.destinationrule.tls-simple-insecure",
			Description: "DestinationRule tls.mode SIMPLE with insecureSkipVerify and sni originates TLS to an external HTTPS backend " +
				"without verifying its certificate.",
			Roles:      l7Roles,
			RequiresL7: true,
			Covers: []string{
				"DestinationRule.trafficPolicy.tls", "DestinationRule.trafficPolicy.tls.mode=SIMPLE", "DestinationRule.trafficPolicy.tls.sni",
				"DestinationRule.trafficPolicy.tls.insecureSkipVerify", "ServiceEntry.resolution=DNS", "ServiceEntry.endpoints", "ServiceEntry.endpoints.address",
				"ServiceEntry.location=MESH_EXTERNAL", "ServiceEntry.ports", "ServiceEntry.ports.targetPort", "ServiceEntry.hosts",
			},
			Run: externalPolicy(`
    tls:
      mode: SIMPLE
      insecureSkipVerify: true
      sni: `+BackendTLSHost+`
`, 443, check.And(FromService(ExternalSvc), check.SNI(BackendTLSHost))),
		},
		Feature{
			ID:          "backend.destinationrule.tls-istio-mutual-external",
			Description: "DestinationRule tls.mode ISTIO_MUTUAL towards a plaintext out-of-mesh backend makes requests fail (503) instead of falling back to plaintext.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"DestinationRule.trafficPolicy.tls.mode=ISTIO_MUTUAL"},
			Run: externalPolicy(`
    tls:
      mode: ISTIO_MUTUAL
`, 80, check.Status(503)),
		},
		Feature{
			ID:          "backend.destinationrule.tls-disable-mesh",
			Description: "DestinationRule tls.mode DISABLE overrides auto mTLS towards a sidecar backend: the backend sees a plaintext request without client identity.",
			Roles:       []Role{RoleSidecar},
			RequiresL7:  true,
			Covers:      []string{"DestinationRule.trafficPolicy.tls.mode=DISABLE", "interaction:mtls.destinationrule-overrides-auto"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone, Shared: true})
				tg.DestinationPolicy(t, `
    tls:
      mode: DISABLE
`)
				// With auto mTLS the backend's sidecar sets X-Forwarded-Client-Cert; plaintext has none.
				tg.ExpectStable(t, Request{}, check.And(check.OK(), RequestHeaderAbsent("X-Forwarded-Client-Cert")))
			},
		},

		// --- loadBalancer.consistentHash variants: requests with the same key stick to one endpoint. ---
		Feature{
			ID:          "backend.destinationrule.consistent-hash-query-param",
			Description: "DestinationRule consistentHash.httpQueryParameterName pins requests with the same query parameter value to one endpoint.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"DestinationRule.trafficPolicy.loadBalancer.consistentHash.httpQueryParameterName"},
			Run: consistentHashPinned(`
    loadBalancer:
      consistentHash:
        httpQueryParameterName: user
`, Request{Path: "/?user=alice"}),
		},
		Feature{
			ID:          "backend.destinationrule.consistent-hash-ring-hash",
			Description: "DestinationRule consistentHash with ringHash.minimumRingSize pins requests with the same header value to one endpoint.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"DestinationRule.trafficPolicy.loadBalancer.consistentHash.ringHash",
				"DestinationRule.trafficPolicy.loadBalancer.consistentHash.ringHash.minimumRingSize",
			},
			Run: consistentHashPinned(`
    loadBalancer:
      consistentHash:
        httpHeaderName: x-user
        ringHash: {minimumRingSize: 1024}
`, Request{Headers: H("x-user", "alice")}),
		},
		Feature{
			ID:          "backend.destinationrule.consistent-hash-maglev",
			Description: "DestinationRule consistentHash with maglev.tableSize pins requests with the same header value to one endpoint.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"DestinationRule.trafficPolicy.loadBalancer.consistentHash.maglev", "DestinationRule.trafficPolicy.loadBalancer.consistentHash.maglev.tableSize",
			},
			Run: consistentHashPinned(`
    loadBalancer:
      consistentHash:
        httpHeaderName: x-user
        maglev: {tableSize: 65537}
`, Request{Headers: H("x-user", "alice")}),
		},
		Feature{
			ID:          "backend.destinationrule.consistent-hash-cookie-path",
			Description: "DestinationRule consistentHash.httpCookie with path issues a Set-Cookie scoped to that path.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"DestinationRule.trafficPolicy.loadBalancer.consistentHash.httpCookie.path"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone, Shared: true})
				tg.DestinationPolicy(t, `
    loadBalancer:
      consistentHash:
        httpCookie:
          name: pcs-affinity
          path: /pcs-scope
          ttl: 60s
`)
				tg.Expect(t, Request{}, check.And(ResponseHeaderContains("Set-Cookie", "pcs-affinity="), ResponseHeaderContains("Set-Cookie", "Path=/pcs-scope")))
			},
		},
		Feature{
			ID:          "backend.destinationrule.workload-selector",
			Description: "A DestinationRule with a workloadSelector applies to the selected client's sidecar.",
			Roles:       []Role{RoleSidecar},
			RequiresL7:  true,
			Covers:      []string{"DestinationRule.workloadSelector", "DestinationRule.workloadSelector.matchLabels"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone})
				tg.RouteToDest(t)
				// The DestinationRule's host is the feature's own backend, so selecting the shared client
				// affects no other feature.
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}
spec:
  host: {{.Dest}}
  workloadSelector:
    matchLabels:
      app: {{.ClientApp}}
  trafficPolicy:
    loadBalancer:
      consistentHash:
        httpHeaderName: x-user
`)
				tg.Expect(t, Request{Count: 20, Headers: H("x-user", "alice")}, check.And(check.OK(), sameHostname()))
			},
		},

		// --- proxyProtocol versions towards the external echo's PROXY-protocol port. ---
		Feature{
			ID:          "backend.destinationrule.proxy-protocol-v2",
			Description: "A DestinationRule proxyProtocol version V2 makes the proxy send a PROXY v2 header to the backend.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"DestinationRule.trafficPolicy.proxyProtocol.version", "DestinationRule.trafficPolicy.proxyProtocol.version=V2"},
			Run: externalPolicy(`
    proxyProtocol:
      version: V2
`, 87, check.And(check.OK(), check.ProxyProtocolVersion("2"))),
		},

		// --- TLS modes, load balancing and upstream protocol ---
		Feature{
			ID:          "backend.destinationrule.tls-mutual",
			Description: "DestinationRule tls.mode MUTUAL presents the client certificate from credentialName to a backend requiring mTLS.",
			Roles:       ingressRole,
			Covers:      []string{"DestinationRule.trafficPolicy.tls.mode=MUTUAL"},
			Run: func(t framework.TestContext, s *Scenario) {
				dns := func(f string) string {
					return file.AsStringOrFail(t, path.Join(env.IstioSrc, "tests/testdata/certs/dns", f))
				}
				s.TLSSecret(t, s.Name("client"), Certs{CA: dns("root-cert.pem"), Cert: dns("cert-chain.pem"), Key: dns("key.pem")})
				// A client key in a DestinationRule credentialName requires Secret read access for the proxy.
				gw := s.NewGateway(t, GatewayOptions{})
				gw.GrantSecretRead(t)
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
  - {number: 80, name: http, protocol: HTTP, targetPort: 4443}
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
      mode: MUTUAL
      credentialName: {{.Cred}}
      sni: {{.TLSHost}}
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}
spec:
  parentRefs: [{name: {{.GW}}}]
  hostnames: ["{{.Host}}"]
  rules:
  - backendRefs:
    - {group: networking.istio.io, kind: Hostname, name: {{.SEHost}}, port: 80}
`, map[string]any{"GW": gw.Name, "SEHost": s.Name() + "." + ExternalHost, "Cred": s.Name("client"), "TLSHost": BackendTLSHost})
				gw.Expect(t, Request{}, FromService(ExternalSvc))
			},
		},
		Feature{
			ID: "backend.destinationrule.tls-files",
			Description: "DestinationRule tls caCertificates, clientCertificate and privateKey read the CA and client credentials " +
				"from files mounted in the proxy, for MUTUAL origination to a backend requiring mTLS.",
			Roles: ingressRole,
			Covers: []string{
				"DestinationRule.trafficPolicy.tls.caCertificates", "DestinationRule.trafficPolicy.tls.clientCertificate",
				"DestinationRule.trafficPolicy.tls.privateKey",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				dns := func(f string) string {
					return file.AsStringOrFail(t, path.Join(env.IstioSrc, "tests/testdata/certs/dns", f))
				}
				// Without caCertificates the proxy verifies against the system roots, which do not
				// trust the echo's test CA; the backend port requires the client certificate.
				gw := s.NewGateway(t, GatewayOptions{Infrastructure: mountGatewayFiles(t, s, map[string]string{
					"ca.crt": dns("root-cert.pem"), "client.crt": dns("cert-chain.pem"), "client.key": dns("key.pem"),
				})})
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
  - {number: 80, name: http, protocol: HTTP, targetPort: 4443}
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
      mode: MUTUAL
      caCertificates: {{.Dir}}/ca.crt
      clientCertificate: {{.Dir}}/client.crt
      privateKey: {{.Dir}}/client.key
      sni: {{.TLSHost}}
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}
spec:
  parentRefs: [{name: {{.GW}}}]
  hostnames: ["{{.Host}}"]
  rules:
  - backendRefs:
    - {group: networking.istio.io, kind: Hostname, name: {{.SEHost}}, port: 80}
`, map[string]any{"GW": gw.Name, "SEHost": s.Name() + "." + ExternalHost, "Dir": gatewayFilesDir, "TLSHost": BackendTLSHost})
				gw.Expect(t, Request{}, FromService(ExternalSvc))
			},
		},
		Feature{
			ID: "backend.destinationrule.tls-crl",
			Description: "DestinationRule tls.caCrl rejects a backend whose certificate the CRL revokes, while a backend " +
				"with an unrevoked certificate from the same CA stays reachable.",
			Roles:  ingressRole,
			Covers: []string{"DestinationRule.trafficPolicy.tls.caCrl"},
			Run: func(t framework.TestContext, s *Scenario) {
				// The echo's built-in certificate is from a CA whose key is not available to sign a CRL, so
				// a second gateway terminates TLS with certificates from a fresh CA: two listeners whose
				// certificates differ only in serial number, one of them revoked. One ServiceEntry port
				// per listener, under a single DestinationRule, so only the certificate differs.
				ca := newCRLCA(t)
				sni := "backend." + s.Host()
				good, revoked := ca.leaf(t, sni, sni), ca.leaf(t, sni, sni)
				s.TLSSecret(t, s.Name("good"), Certs{Cert: good.Cert, Key: good.Key})
				s.TLSSecret(t, s.Name("revoked"), Certs{Cert: revoked.Cert, Key: revoked.Key})
				backend := s.NewGateway(t, GatewayOptions{Suffix: "backend", Listeners: `
  - name: good
    port: 443
    protocol: HTTPS
    tls:
      mode: Terminate
      certificateRefs: [{name: {{.Good}}}]
  - name: revoked
    port: 8443
    protocol: HTTPS
    tls:
      mode: Terminate
      certificateRefs: [{name: {{.Revoked}}}]
`, ListenerVars: map[string]any{"Good": s.Name("good"), "Revoked": s.Name("revoked")}})
				var backendAddr string
				EventuallyKube(t, func() error {
					svc, err := backend.Service(t)
					if err != nil {
						return err
					}
					backendAddr = svc.Name + "." + svc.Namespace + ".svc.cluster.local"
					return nil
				})
				gw := s.NewGateway(t, GatewayOptions{Infrastructure: mountGatewayFiles(t, s, map[string]string{
					"ca.crt": ca.pem, "ca.crl": ca.crl(t, revoked),
				})})
				s.Apply(t, `
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}-backend
spec:
  parentRefs: [{name: {{.Backend}}}]
  rules:
  - backendRefs:
    - {name: {{.BackendV1}}, port: 80}
---
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.SEHost}}"]
  location: MESH_EXTERNAL
  resolution: DNS
  ports:
  - {number: 80, name: http-good, protocol: HTTP, targetPort: 443}
  - {number: 81, name: http-revoked, protocol: HTTP, targetPort: 8443}
  endpoints:
  - address: {{.BackendAddr}}
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
      caCertificates: {{.Dir}}/ca.crt
      caCrl: {{.Dir}}/ca.crl
      sni: {{.SNI}}
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}
spec:
  parentRefs: [{name: {{.GW}}}]
  hostnames: ["good.{{.Host}}"]
  rules:
  - backendRefs:
    - {group: networking.istio.io, kind: Hostname, name: {{.SEHost}}, port: 80}
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}-revoked
spec:
  parentRefs: [{name: {{.GW}}}]
  hostnames: ["revoked.{{.Host}}"]
  rules:
  - backendRefs:
    - {group: networking.istio.io, kind: Hostname, name: {{.SEHost}}, port: 81}
`, map[string]any{
					"GW": gw.Name, "Backend": backend.Name, "BackendAddr": backendAddr, "SEHost": s.Name() + "." + ExternalHost,
					"Dir": gatewayFilesDir, "SNI": sni,
				})
				// The unrevoked listener proves the CA, DestinationRule and both ports are in effect.
				gw.Expect(t, Request{Host: "good." + s.Host()}, FromService(BackendSvc))
				gw.ExpectStable(t, Request{Host: "revoked." + s.Host()}, some5xx())
			},
		},
		Feature{
			ID:          "backend.destinationrule.tls-disable-and-istio-mutual",
			Description: "DestinationRule tls.mode DISABLE sends plaintext to a sidecar backend (no client identity); ISTIO_MUTUAL sends mesh mTLS.",
			Roles:       ingressRole,
			Covers:      []string{"DestinationRule.trafficPolicy.tls.mode=DISABLE", "DestinationRule.trafficPolicy.tls.mode=ISTIO_MUTUAL"},
			Run: func(t framework.TestContext, s *Scenario) {
				for _, mode := range []string{"DISABLE", "ISTIO_MUTUAL"} {
					label := strings.ReplaceAll(strings.ToLower(mode), "_", "-")
					host := label + "." + s.Name() + ".alias.pcs.example.com"
					s.Apply(t, backendAlias(host, `
    tls:
      mode: `+mode+`
`), s.sharedVars(), map[string]any{"SEHost": host, "Name": s.Name(label), "Host": label + "." + s.Host()})
				}
				gw := s.SharedGateway()
				gw.ExpectStable(t, Request{Host: "disable." + s.Host()}, check.And(check.OK(), RequestHeaderAbsent("X-Forwarded-Client-Cert")))
				gw.Expect(t, Request{Host: "istio-mutual." + s.Host()}, RequestHeaderPresent("X-Forwarded-Client-Cert"))
			},
		},
		Feature{
			ID:          "backend.destinationrule.consistent-hash-source-ip",
			Description: "DestinationRule consistentHash.useSourceIp pins a client to one endpoint.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"DestinationRule.trafficPolicy.loadBalancer", "DestinationRule.trafficPolicy.loadBalancer.consistentHash.useSourceIp"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone, Shared: true})
				tg.DestinationPolicy(t, `
    loadBalancer:
      consistentHash:
        useSourceIp: true
`)
				tg.Expect(t, Request{Count: 20}, check.And(check.OK(), sameHostname()))
			},
		},
		Feature{
			ID:          "backend.destinationrule.consistent-hash-cookie",
			Description: "DestinationRule consistentHash.httpCookie with ttl issues a Set-Cookie for session affinity.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"DestinationRule.trafficPolicy.loadBalancer.consistentHash.httpCookie",
				"DestinationRule.trafficPolicy.loadBalancer.consistentHash.httpCookie.name",
				"DestinationRule.trafficPolicy.loadBalancer.consistentHash.httpCookie.ttl",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone, Shared: true})
				tg.DestinationPolicy(t, `
    loadBalancer:
      consistentHash:
        httpCookie:
          name: pcs-affinity
          ttl: 60s
`)
				tg.Expect(t, Request{}, ResponseHeaderContains("Set-Cookie", "pcs-affinity="))
			},
		},
		Feature{
			ID:          "backend.destinationrule.h2-upgrade",
			Description: "DestinationRule connectionPool.http.h2UpgradePolicy UPGRADE makes the gateway talk HTTP/2 to an HTTP/1.1-declared backend port.",
			Roles:       ingressRole,
			Covers: []string{
				"DestinationRule.trafficPolicy.connectionPool.http.h2UpgradePolicy", "DestinationRule.trafficPolicy.connectionPool.http.h2UpgradePolicy=UPGRADE",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				// Out-of-mesh backend: an in-mesh sidecar would re-encode the request toward the app.
				s.Apply(t, externalAlias(`
    connectionPool:
      http:
        h2UpgradePolicy: UPGRADE
`), s.sharedVars())
				s.SharedGateway().Expect(t, Request{}, check.And(check.OK(), check.Protocol("HTTP/2.0")))
			},
		},
		Feature{
			ID:          "backend.destinationrule.use-client-protocol",
			Description: "DestinationRule connectionPool.http.useClientProtocol makes the upstream protocol follow the client's.",
			Roles:       ingressRole,
			Covers:      []string{"DestinationRule.trafficPolicy.connectionPool.http.useClientProtocol"},
			Run: func(t framework.TestContext, s *Scenario) {
				s.Apply(t, externalAlias(`
    connectionPool:
      http:
        useClientProtocol: true
`), s.sharedVars())
				gw := s.SharedGateway()
				gw.Expect(t, Request{HTTP2: true}, check.And(check.OK(), check.Protocol("HTTP/2.0")))
				gw.Expect(t, Request{}, check.And(check.OK(), check.Protocol("HTTP/1.1")))
			},
		},

		// --- Policy precedence: subset- and port-level traffic policies ---
		Feature{
			ID:          "backend.destinationrule.subset-traffic-policy",
			Description: "A subset-level trafficPolicy overrides the top-level policy for that subset only (subset v1 plaintext, v2 mesh mTLS).",
			Roles:       ingressRole,
			Covers:      []string{"DestinationRule.subsets.trafficPolicy"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := istioGatewayBinding(t, s)
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}
spec:
  host: {{.Backend}}
  exportTo: ["."]
  trafficPolicy:
    tls: {mode: ISTIO_MUTUAL}
  subsets:
  - name: v1
    labels: {version: v1}
    trafficPolicy:
      tls: {mode: DISABLE}
  - name: v2
    labels: {version: v2}
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["vs.{{.Host}}"]
  gateways: ["{{.Name}}-istio"]
  http:
  - match: [{headers: {x-subset: {exact: v2}}}]
    route: [{destination: {host: {{.Backend}}, subset: v2, port: {number: 80}}}]
  - route: [{destination: {host: {{.Backend}}, subset: v1, port: {number: 80}}}]
`)
				h := "vs." + s.Host()
				gw.ExpectStable(t, Request{Host: h}, check.And(Version("v1"), RequestHeaderAbsent("X-Forwarded-Client-Cert")))
				gw.Expect(t, Request{Host: h, Headers: H("x-subset", "v2")}, check.And(Version("v2"), RequestHeaderPresent("X-Forwarded-Client-Cert")))
			},
		},
		Feature{
			ID:          "backend.destinationrule.port-level-settings",
			Description: "trafficPolicy.portLevelSettings applies a policy to one destination port only (port 80 plaintext, port 85 mesh mTLS).",
			Roles:       ingressRole,
			Covers: []string{
				"DestinationRule.trafficPolicy.portLevelSettings", "DestinationRule.trafficPolicy.portLevelSettings.port",
				"DestinationRule.trafficPolicy.portLevelSettings.port.number",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				host := s.Name() + ".ports.pcs.example.com"
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.SEHost}}"]
  location: MESH_INTERNAL
  resolution: STATIC
  workloadSelector:
    labels: {app: {{.Backend}}}
  ports:
  - {number: 80, name: http, protocol: HTTP, targetPort: 18080}
  - {number: 85, name: http2, protocol: HTTP, targetPort: 18085}
---
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}
spec:
  host: {{.SEHost}}
  trafficPolicy:
    portLevelSettings:
    - port: {number: 80}
      tls: {mode: DISABLE}
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}
spec:
  parentRefs: [{name: {{.SharedGateway}}}]
  hostnames: ["{{.Host}}"]
  rules:
  - matches: [{path: {type: PathPrefix, value: /p85}}]
    backendRefs: [{group: networking.istio.io, kind: Hostname, name: {{.SEHost}}, port: 85}]
  - backendRefs: [{group: networking.istio.io, kind: Hostname, name: {{.SEHost}}, port: 80}]
`, s.sharedVars(), map[string]any{"SEHost": host})
				gw := s.SharedGateway()
				gw.ExpectStable(t, Request{}, check.And(check.OK(), RequestHeaderAbsent("X-Forwarded-Client-Cert")))
				gw.Expect(t, Request{Path: "/p85"}, RequestHeaderPresent("X-Forwarded-Client-Cert"))
			},
		},
	)
}

// backendAlias returns config for a per-feature ServiceEntry alias of the in-mesh backend (host
// .SEHost) with a DestinationRule trafficPolicy (YAML indented for trafficPolicy), and an HTTPRoute on
// the shared gateway for .Host. Apply it with s.sharedVars() and {"SEHost": host}.
func backendAlias(_ string, trafficPolicy string) string {
	return `
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.SEHost}}"]
  location: MESH_INTERNAL
  resolution: STATIC
  workloadSelector:
    labels:
      app: {{.Backend}}
  ports:
  - {number: 80, name: http, protocol: HTTP, targetPort: 18080}
---
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}
spec:
  host: {{.SEHost}}
  trafficPolicy:
` + trafficPolicy + `
---
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
`
}

// externalAlias is like backendAlias, but for the out-of-mesh backend (ServiceEntry DNS to the
// external Service), so the backend observes exactly what the gateway sends.
func externalAlias(trafficPolicy string) string {
	return externalServiceEntry() + `---
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}
spec:
  host: "{{.Name}}.{{.ExternalHost}}"
  trafficPolicy:
` + trafficPolicy + `
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}
spec:
  parentRefs: [{name: {{.SharedGateway}}}]
  hostnames: ["{{.Host}}"]
  rules:
  - backendRefs:
    - {group: networking.istio.io, kind: Hostname, name: "{{.Name}}.{{.ExternalHost}}", port: 80}
`
}

// connectionPoolOverflow is the body of the connectionPool family: with the limits in policy, 20
// concurrent slow requests must partly overflow (503) while some succeed. Without the policy every
// request succeeds (verified by the negative control).
func connectionPoolOverflow(policy string) func(t framework.TestContext, s *Scenario) {
	return func(t framework.TestContext, s *Scenario) {
		tg := s.Target(t, TargetOptions{Routing: RouteNone, Shared: true})
		tg.DestinationPolicy(t, policy)
		// The echo client sends the requests of one call concurrently.
		tg.Expect(t, Request{Path: "/?delay=1s", Count: 20}, check.And(someStatus(200), someStatus(503)))
	}
}

// externalPolicy is the body of the external-destination family: the target host is routed to a
// per-feature ServiceEntry for the out-of-mesh echo on the given service port (80 plaintext HTTP, 443
// TLS, 87 PROXY protocol), and policy (a DestinationRule trafficPolicy) must produce want. Without the
// policy the request reaches the port unmodified, which none of the wanted results allow.
func externalPolicy(policy string, port int, want echo.Checker) func(t framework.TestContext, s *Scenario) {
	return func(t framework.TestContext, s *Scenario) {
		tg := s.Target(t, TargetOptions{Routing: RouteNone, Shared: true})
		host := tg.ServiceEntry(t, `
  location: MESH_EXTERNAL
  resolution: DNS
  ports:
  - {number: 80, name: http, protocol: HTTP, targetPort: `+itoa(port)+`}
  endpoints:
  - address: {{.External}}.{{.ExternalNamespace}}.svc.cluster.local
`)
		s.Apply(t, destinationRule(policy), map[string]any{"PolicyHost": host})
		if port == 80 {
			// The plaintext port works without the policy; the failure must be stable.
			tg.ExpectStable(t, Request{}, want)
			return
		}
		tg.Expect(t, Request{}, want)
	}
}

// consistentHashPinned is the body of the consistentHash family: with policy, 20 requests r with the
// same hash key reach a single endpoint of the two backend versions.
func consistentHashPinned(policy string, r Request) func(t framework.TestContext, s *Scenario) {
	return func(t framework.TestContext, s *Scenario) {
		tg := s.Target(t, TargetOptions{Routing: RouteNone, Shared: true})
		tg.DestinationPolicy(t, policy)
		r.Count = 20
		tg.Expect(t, r, check.And(check.OK(), sameHostname()))
	}
}

// someStatus checks that at least one response has the status code.
func someStatus(code int) echo.Checker {
	return func(result echo.CallResult, err error) error {
		got := map[string]int{}
		for _, r := range result.Responses {
			got[r.Code]++
		}
		if got[fmt.Sprint(code)] == 0 {
			return fmt.Errorf("expected at least one response with status %d, got %v (err: %v)", code, got, err)
		}
		return nil
	}
}

// some5xx checks that at least one response was a 5xx. An upstream protocol error is a 502
// (Envoy's UPE behavior since 1.18); a connection failure is a 503.
func some5xx() echo.Checker {
	return func(result echo.CallResult, err error) error {
		got := map[string]int{}
		for _, r := range result.Responses {
			got[r.Code]++
			if len(r.Code) == 3 && r.Code[0] == '5' {
				return nil
			}
		}
		return fmt.Errorf("expected at least one 5xx response, got %v (err: %v)", got, err)
	}
}
