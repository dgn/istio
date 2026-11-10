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
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"

	"istio.io/istio/pkg/test/env"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo/check"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/util/file"
	"istio.io/istio/pkg/test/util/retry"
	"istio.io/istio/tests/common/jwt"
)

// Additional coverage, driven by the coverage/exemptions.yaml backlog.
func init() {
	Register(
		// ---- AuthorizationPolicy ----
		Feature{
			ID:          "security.authz.audit",
			Description: "An AUDIT AuthorizationPolicy never changes the response.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"AuthorizationPolicy.action=AUDIT"},
			NoControl:   "AUDIT has no client-visible effect by definition; only audit logs would show it",
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.AuthorizationPolicy("AUDIT", `
  - {}
`))
				tg.ExpectStable(t, Request{}, check.OK())
			},
		},
		Feature{
			ID:          "security.authz.source-identity-plaintext",
			Description: "Plaintext (non-mesh) clients have no principal/namespace/service account: ALLOW rules on them deny, DENY rules on their absence (not*) deny.",
			Roles:       ingressRole,
			Covers: []string{"AuthorizationPolicy.rules.from.source.namespaces", "AuthorizationPolicy.rules.from.source.serviceAccounts",
				"AuthorizationPolicy.rules.from.source.notPrincipals", "AuthorizationPolicy.rules.from.source.notNamespaces"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := policyGateway(t, s, "deny")
				s.Apply(t, authzPolicy(gw, "ALLOW", `
  - from:
    - source:
        namespaces: ["{{.Namespace}}"]
  - from:
    - source:
        serviceAccounts: ["{{.Namespace}}/client"]
  - to:
    - operation:
        hosts: ["deny.{{.Host}}"]
`, "allow"))
				s.Apply(t, authzPolicy(gw, "DENY", `
  - from:
    - source:
        notPrincipals: ["*"]
        notNamespaces: ["*"]
    to:
    - operation:
        hosts: ["deny.{{.Host}}"]
`, "deny"))
				gw.Expect(t, Request{}, check.Status(403))
				gw.Expect(t, Request{Host: "deny." + s.Host()}, check.Status(403))
			},
		},
		Feature{
			ID:          "security.authz.not-request-principals",
			Description: "DENY with notRequestPrincipals ['*'] requires a valid JWT.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"AuthorizationPolicy.rules.from.source.notRequestPrincipals"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.RequestAuthentication(""))
				s.Apply(t, tg.AuthorizationPolicy("DENY", `
  - from:
    - source:
        notRequestPrincipals: ["*"]
`))
				tg.Expect(t, Request{}, check.Status(403))
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1)}, check.OK())
			},
		},
		Feature{
			ID:          "security.authz.not-ip-blocks",
			Description: "notRemoteIpBlocks/notIpBlocks deny everything outside the listed ranges.",
			Roles:       ingressRole,
			Covers:      []string{"AuthorizationPolicy.rules.from.source.notRemoteIpBlocks", "AuthorizationPolicy.rules.from.source.notIpBlocks"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := policyGateway(t, s)
				s.Apply(t, authzPolicy(gw, "DENY", `
  - from:
    - source:
        notRemoteIpBlocks: ["192.0.2.1/32"]
    to:
    - operation:
        paths: ["/remote"]
  - from:
    - source:
        notIpBlocks: ["192.0.2.1/32"]
    to:
    - operation:
        paths: ["/peer"]
`))
				pod := gw.PodIP(t)
				gw.Expect(t, Request{Address: pod, Path: "/remote"}, check.Status(403))
				gw.Expect(t, Request{Address: pod, Path: "/peer"}, check.Status(403))
				gw.Expect(t, Request{Address: pod, Path: "/other"}, check.OK())
			},
		},
		Feature{
			ID:          "security.authz.not-hosts-not-values",
			Description: "notHosts and when.notValues negative matches are honored.",
			Roles:       ingressRole,
			Covers:      []string{"AuthorizationPolicy.rules.to.operation.notHosts", "AuthorizationPolicy.rules.when.notValues"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := policyGateway(t, s, "other")
				s.Apply(t, authzPolicy(gw, "DENY", `
  - to:
    - operation:
        notHosts: ["{{.Host}}"]
  - when:
    - key: request.headers[x-token]
      notValues: ["secret"]
    to:
    - operation:
        paths: ["/guarded"]
`))
				gw.Expect(t, Request{Host: "other." + s.Host()}, check.Status(403))
				gw.Expect(t, Request{Path: "/guarded"}, check.Status(403))
				gw.Expect(t, Request{Path: "/guarded", Headers: H("x-token", "secret")}, check.OK())
			},
		},
		Feature{
			ID:          "security.authz.namespace-wide",
			Description: "An AuthorizationPolicy without selector or targetRefs in the gateway's namespace applies to the gateway.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := externalPolicyGateway(t, s)
				path := "/pcs-ns-deny-" + s.Name()
				s.Apply(t, `
apiVersion: security.istio.io/v1
kind: AuthorizationPolicy
metadata:
  name: {{.Name}}
spec:
  action: DENY
  rules:
  - to:
    - operation:
        paths: ["{{.P}}"]
`, map[string]any{"P": path})
				gw.Expect(t, Request{Path: path}, check.Status(403))
			},
		},
		Feature{
			ID:          "security.authz.root-namespace",
			Description: "A mesh-wide AuthorizationPolicy in the root namespace applies to the gateway.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := externalPolicyGateway(t, s)
				root := istio.ClaimSystemNamespaceOrFail(t, t).Name()
				path := "/pcs-mesh-deny-" + s.Name()
				s.ApplyTo(t, root, `
apiVersion: security.istio.io/v1
kind: AuthorizationPolicy
metadata:
  name: {{.Name}}
spec:
  action: DENY
  rules:
  - to:
    - operation:
        paths: ["{{.P}}"]
`, map[string]any{"P": path})
				gw.Expect(t, Request{Path: path}, check.Status(403))
				gw.Expect(t, Request{}, check.OK())
			},
		},
		Feature{
			ID:          "security.authz.targetref-scoped",
			Description: "A policy targeting one Gateway does not affect another Gateway in the same namespace.",
			Roles:       ingressRole,
			Covers:      []string{"AuthorizationPolicy.targetRefs.kind", "AuthorizationPolicy.targetRefs.group", "AuthorizationPolicy.targetRefs.name"},
			Run: func(t framework.TestContext, s *Scenario) {
				a := policyGateway(t, s)
				b := s.NewGateway(t, GatewayOptions{Suffix: "b"})
				s.Apply(t, httpRouteTo(b, "{{.BackendV1}}", 80), map[string]any{"Name": s.Name("b")})
				s.Apply(t, authzPolicy(a, "DENY", `
  - {}
`))
				a.Expect(t, Request{}, check.Status(403))
				b.ExpectStable(t, Request{}, check.OK())
			},
		},
		Feature{
			ID:          "security.authz.custom-ext-authz-headers",
			Description: "CUSTOM ext-authz headersToUpstreamOnAllow/headersToDownstreamOnDeny propagate the authorizer's headers.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, `
apiVersion: security.istio.io/v1
kind: AuthorizationPolicy
metadata:
  name: {{.Name}}
spec:
{{.PolicyAttachment}}
  action: CUSTOM
  provider: {name: ext-authz-http}
  rules: [{}]
`)
				tg.Expect(t, Request{Headers: H("x-ext-authz", "allow")}, RequestHeaderPresent("X-Ext-Authz-Check-Result"))
				tg.Expect(t, Request{Headers: H("x-ext-authz", "deny")}, check.And(check.Status(403), ResponseHeaderPresent("X-Ext-Authz-Check-Result")))
			},
		},

		// ---- RequestAuthentication ----
		Feature{
			ID:          "security.jwt.presence-required",
			Description: "RequestAuthentication jwtRules presence REQUIRED rejects requests without a token (401).",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"RequestAuthentication.jwtRules.presence", "RequestAuthentication.jwtRules.presence=REQUIRED"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.RequestAuthentication(`
    presence: REQUIRED
`))
				tg.Expect(t, Request{}, check.Status(401))
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1)}, check.OK())
			},
		},
		Feature{
			ID:          "security.jwt.from-cookies",
			Description: "RequestAuthentication fromCookies extracts the token from a cookie.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"RequestAuthentication.jwtRules.fromCookies"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.RequestAuthentication(`
    fromCookies: ["session"]
`))
				s.Apply(t, tg.AuthorizationPolicy("ALLOW", `
  - from:
    - source:
        requestPrincipals: ["*"]
`))
				tg.Expect(t, Request{}, check.Status(403))
				tg.Expect(t, Request{Headers: H("Cookie", "session="+jwt.TokenIssuer1)}, check.OK())
			},
		},
		Feature{
			ID:          "security.jwt.space-delimited-claims",
			Description: "Space-delimited claims (scope) are split for claim matching in AuthorizationPolicy.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"RequestAuthentication.jwtRules.spaceDelimitedClaims"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, `
apiVersion: security.istio.io/v1
kind: RequestAuthentication
metadata:
  name: {{.Name}}-jwt
spec:
{{.PolicyAttachment}}
  jwtRules:
  - issuer: test-issuer-2@istio.io
    jwks: '{{.JWKS}}'
    spaceDelimitedClaims: ["scope"]
`, map[string]any{"JWKS": jwt.JwksJSON})
				s.Apply(t, tg.AuthorizationPolicy("ALLOW", `
  - when:
    - key: request.auth.claims[scope]
      values: ["read"]
`))
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer2WithSpaceDelimitedScope)}, check.OK())
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer2)}, check.Status(403))
			},
		},
		Feature{
			ID:          "security.jwt.selector",
			Description: "RequestAuthentication attached by workload selector (the gateway pods, or the server workload of a sidecar) is enforced.",
			Roles:       []Role{RoleIngress, RoleSidecar},
			RequiresL7:  true,
			Covers:      []string{"RequestAuthentication.selector", "RequestAuthentication.selector.matchLabels"},
			// Not waypoint: selector policies never bind to a waypoint.
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, `
apiVersion: security.istio.io/v1
kind: RequestAuthentication
metadata:
  name: {{.Name}}-jwt
spec:
  selector:
    matchLabels:
      {{.WorkloadSelector}}
  jwtRules:
  - issuer: test-issuer-1@istio.io
    jwks: '{{.JWKS}}'
`, map[string]any{"JWKS": jwt.JwksJSON})
				tg.Expect(t, Request{Headers: bearer(jwt.TokenInvalid)}, check.Status(401))
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1)}, check.OK())
			},
		},

		// ---- PeerAuthentication ----
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

		// ---- DestinationRule ----
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
			ID:          "backend.destinationrule.lb-round-robin",
			Description: "DestinationRule loadBalancer.simple ROUND_ROBIN spreads requests evenly across endpoints.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"DestinationRule.trafficPolicy.loadBalancer.simple", "DestinationRule.trafficPolicy.loadBalancer.simple=ROUND_ROBIN"},
			NoControl: "for sequential requests ROUND_ROBIN and the default balancer are not reliably distinguishable black-box " +
				"(Envoy keeps per-worker LB state, so even round robin does not strictly alternate); the feature only checks the policy is accepted and balances",
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone, Shared: true})
				tg.DestinationPolicy(t, `
    loadBalancer:
      simple: ROUND_ROBIN
`)
				tg.Expect(t, Request{Count: SampleSize(50, 50)}, Distribution(map[string]int{"v1": 50, "v2": 50}))
			},
		},
		Feature{
			ID:          "backend.destinationrule.consistent-hash-source-ip",
			Description: "DestinationRule consistentHash.useSourceIp pins a client to one endpoint.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"DestinationRule.trafficPolicy.loadBalancer.consistentHash.useSourceIp"},
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
			Covers:      []string{"DestinationRule.trafficPolicy.loadBalancer.consistentHash.httpCookie", "DestinationRule.trafficPolicy.loadBalancer.consistentHash.httpCookie.name", "DestinationRule.trafficPolicy.loadBalancer.consistentHash.httpCookie.ttl"},
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
			Covers:      []string{"DestinationRule.trafficPolicy.connectionPool.http.h2UpgradePolicy", "DestinationRule.trafficPolicy.connectionPool.http.h2UpgradePolicy=UPGRADE"},
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
		Feature{
			ID:          "backend.serviceentry.dns-round-robin",
			Description: "A ServiceEntry with DNS_ROUND_ROBIN resolution is routable through the gateway.",
			Roles:       ingressRole,
			Covers:      []string{"ServiceEntry.resolution=DNS_ROUND_ROBIN"},
			Run: func(t framework.TestContext, s *Scenario) {
				s.Apply(t, strings.Replace(externalServiceEntry(80, "HTTP"), "resolution: DNS", "resolution: DNS_ROUND_ROBIN", 1))
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

		// ---- ProxyConfig ----
		Feature{
			ID:          "dataplane.xfcc.always-forward-only",
			Description: "gatewayTopology.forwardClientCertDetails ALWAYS_FORWARD_ONLY forwards a client-supplied X-Forwarded-Client-Cert even on plaintext connections (APPEND_FORWARD only does so for mTLS clients).",
			Roles:       ingressRole,
			Covers:      []string{"ProxyConfig.gatewayTopology.forwardClientCertDetails", "ProxyConfig.gatewayTopology.forwardClientCertDetails=ALWAYS_FORWARD_ONLY"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{Annotations: map[string]string{
					"proxy.istio.io/config": `{"gatewayTopology":{"forwardClientCertDetails":"ALWAYS_FORWARD_ONLY"}}`,
				}})
				s.Apply(t, externalRoute(gw))
				gw.Expect(t, Request{Headers: H("X-Forwarded-Client-Cert", "By=spiffe://upstream/ns/x/sa/y;Hash=0")},
					RequestHeaderContains("X-Forwarded-Client-Cert", "spiffe://upstream"))
			},
		},

		// ---- Telemetry ----
		Feature{
			ID:          "telemetry.metrics.disabled",
			Description: "A Telemetry metrics override with disabled: true removes istio_requests_total for the gateway.",
			Roles:       ingressRole,
			Covers:      []string{"Telemetry.metrics.overrides.disabled", "Telemetry.metrics.overrides.match.metric", "Telemetry.metrics.overrides.match.metric=REQUEST_COUNT"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				s.Apply(t, httpRouteTo(gw, "{{.BackendV1}}", 80))
				// Baseline: the metric must exist before it can be disabled.
				retry.UntilSuccessOrFail(t, func() error {
					if _, err := gw.Call(t, Request{}); err != nil {
						return err
					}
					m, err := gw.Metrics(t)
					if err != nil {
						return err
					}
					if !strings.Contains(m, "istio_requests_total{") {
						return fmt.Errorf("baseline: no istio_requests_total before disabling")
					}
					return nil
				}, retry.Timeout(time.Minute), retry.Delay(2*time.Second))
				nonce := rand.String(8)
				s.Apply(t, telemetryFor(gw, `
  metrics:
  - providers: [{name: prometheus}]
    overrides:
    - match: {metric: REQUEST_COUNT}
      disabled: true
`+WitnessMetricsOverride(nonce)))
				gw.WitnessTelemetry(t, nonce)
				// Existing counters may keep being exported; disabled means they stop increasing. The
				// witness histogram counts the same requests and is updated after counters, so once it
				// includes the batch, istio_requests_total would have too.
				m, err := gw.Metrics(t)
				if err != nil {
					t.Fatal(err)
				}
				before, witnessBefore := requestsTotalIn(m), witnessCount(m, nonce)
				gw.Expect(t, Request{Count: 5}, check.OK())
				retry.UntilSuccessOrFail(t, func() error {
					if m, err = gw.Metrics(t); err != nil {
						return err
					}
					if got := witnessCount(m, nonce); got < witnessBefore+5 {
						return fmt.Errorf("witness histogram has %v of %v requests", got-witnessBefore, 5)
					}
					return nil
				}, retry.Timeout(time.Minute), retry.Delay(time.Second))
				if after := requestsTotalIn(m); after != before {
					t.Fatalf("istio_requests_total kept increasing after disabling REQUEST_COUNT (%v -> %v)", before, after)
				}
			},
		},
		Feature{
			ID:          "telemetry.metrics.tag-remove",
			Description: "A Telemetry tagOverrides REMOVE drops a standard label from istio_requests_total.",
			Roles:       ingressRole,
			Covers:      []string{"Telemetry.metrics.overrides.tagOverrides.operation", "Telemetry.metrics.overrides.tagOverrides.operation=REMOVE"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				s.Apply(t, httpRouteTo(gw, "{{.BackendV1}}", 80))
				s.Apply(t, telemetryFor(gw, `
  metrics:
  - providers: [{name: prometheus}]
    overrides:
    - match: {metric: REQUEST_COUNT}
      tagOverrides:
        request_protocol:
          operation: REMOVE
`))
				retry.UntilSuccessOrFail(t, func() error {
					if _, err := gw.Call(t, Request{}); err != nil {
						return err
					}
					m, err := gw.Metrics(t)
					if err != nil {
						return err
					}
					for _, l := range strings.Split(m, "\n") {
						if strings.HasPrefix(l, "istio_requests_total{") {
							if strings.Contains(l, "request_protocol=") {
								return fmt.Errorf("request_protocol label still present")
							}
							return nil
						}
					}
					return fmt.Errorf("no istio_requests_total series")
				}, retry.Timeout(2*time.Minute), retry.Delay(2*time.Second))
			},
		},
		Feature{
			ID:          "telemetry.accesslog.selector",
			Description: "A Telemetry resource attached by selector (gateway pod labels) disables access logs.",
			Roles:       ingressRole,
			Covers:      []string{"Telemetry.selector", "Telemetry.selector.matchLabels"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				s.Apply(t, httpRouteTo(gw, "{{.BackendV1}}", 80))
				nonce := rand.String(8)
				s.Apply(t, `
apiVersion: telemetry.istio.io/v1
kind: Telemetry
metadata:
  name: {{.Name}}
spec:
  selector:
    matchLabels:
      gateway.networking.k8s.io/gateway-name: {{.GW}}
  accessLogging:
  - disabled: true
{{.Witness}}`, map[string]any{"GW": gw.Name, "Witness": WitnessMetrics(nonce)})
				gw.WitnessTelemetry(t, nonce)
				marker := "/pcs-nolog-" + rand.String(10)
				gw.Expect(t, Request{Path: marker, Count: 3}, check.OK())
				expectNoGatewayLog(t, gw, marker, "")
			},
		},

		// ---- Infrastructure / status ----
		Feature{
			ID:          "infra.parametersref-hpa-pdb-serviceaccount",
			Description: "parametersRef overlays for horizontalPodAutoscaler, podDisruptionBudget and serviceAccount create/patch those resources.",
			Roles:       ingressRole,
			Covers: []string{"gateway-api-ext:parametersref-overlay:horizontalPodAutoscaler", "gateway-api-ext:parametersref-overlay:podDisruptionBudget",
				"gateway-api-ext:parametersref-overlay:serviceAccount"},
			Run: func(t framework.TestContext, s *Scenario) {
				s.Apply(t, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{.Name}}-params
data:
  horizontalPodAutoscaler: |
    spec:
      minReplicas: 1
      maxReplicas: 2
  podDisruptionBudget: |
    spec:
      minAvailable: 1
  serviceAccount: |
    metadata:
      annotations:
        pcs.istio.io/sa-overlay: "yes"
`)
				gw := s.NewGateway(t, GatewayOptions{Infrastructure: `
    parametersRef: {group: "", kind: ConfigMap, name: ` + s.Name() + `-params}
`})
				kc := t.Clusters().Default().Kube()
				EventuallyKube(t, func() error {
					opts := metav1.ListOptions{LabelSelector: gw.Selector()}
					hpas, err := kc.AutoscalingV2().HorizontalPodAutoscalers(gw.Namespace).List(context.Background(), opts)
					if err != nil || len(hpas.Items) != 1 {
						return fmt.Errorf("want 1 HPA, got %v (%v)", len(hpas.Items), err)
					}
					pdbs, err := kc.PolicyV1().PodDisruptionBudgets(gw.Namespace).List(context.Background(), opts)
					if err != nil || len(pdbs.Items) != 1 {
						return fmt.Errorf("want 1 PDB, got %v (%v)", len(pdbs.Items), err)
					}
					sa, err := kc.CoreV1().ServiceAccounts(gw.Namespace).Get(context.Background(), gw.ServiceAccount(t), metav1.GetOptions{})
					if err != nil {
						return err
					}
					if sa.Annotations["pcs.istio.io/sa-overlay"] != "yes" {
						return fmt.Errorf("serviceAccount overlay not applied: %v", sa.Annotations)
					}
					return nil
				})
			},
		},
		Feature{
			ID:          "infra.gatewayclass-defaults-configmap",
			Description: "A ConfigMap in the root namespace labeled gateway.istio.io/defaults-for-class=<class> applies overlays to every gateway of that class.",
			Roles:       ingressRole,
			Covers:      []string{"gateway-api-ext:gatewayclass-defaults-configmap:gateway.istio.io/defaults-for-class"},
			Run: func(t framework.TestContext, s *Scenario) {
				root := istio.ClaimSystemNamespaceOrFail(t, t).Name()
				// A harmless, class-specific Service annotation, so concurrent features are unaffected.
				s.ApplyTo(t, root, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{.Name}}-class-defaults
  labels:
    gateway.istio.io/defaults-for-class: {{.GatewayClass}}
data:
  service: |
    metadata:
      annotations:
        pcs.istio.io/class-default: {{.GatewayClass}}
`)
				gw := s.NewGateway(t, GatewayOptions{})
				EventuallyKube(t, func() error {
					svc, err := gw.Service(t)
					if err != nil {
						return err
					}
					if svc.Annotations["pcs.istio.io/class-default"] != s.Proxy.GatewayClass {
						return fmt.Errorf("class defaults not applied: %v", svc.Annotations)
					}
					return nil
				})
			},
		},
		Feature{
			ID:          "status.address-type-annotation",
			Description: "networking.istio.io/address-type: IPAddress makes Gateway status report IP addresses.",
			Roles:       ingressRole,
			Covers:      []string{"annotation:networking.istio.io/address-type"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{Annotations: map[string]string{
					"networking.istio.io/address-type": "IPAddress",
					"networking.istio.io/service-type": "ClusterIP",
				}})
				EventuallyKube(t, func() error {
					obj, err := gw.Get(t)
					if err != nil {
						return err
					}
					svc, err := gw.Service(t)
					if err != nil {
						return err
					}
					// The Service is ClusterIP, so without the annotation the address would be its hostname.
					for _, a := range obj.Status.Addresses {
						if a.Type != nil && *a.Type == "IPAddress" && a.Value == svc.Spec.ClusterIP {
							return nil
						}
					}
					return fmt.Errorf("status addresses %+v do not include the ClusterIP %s as IPAddress", obj.Status.Addresses, svc.Spec.ClusterIP)
				})
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

// DestinationRule policy precedence (subset- and port-level traffic policies).
func init() {
	Register(
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
			Covers: []string{"DestinationRule.trafficPolicy.portLevelSettings", "DestinationRule.trafficPolicy.portLevelSettings.port",
				"DestinationRule.trafficPolicy.portLevelSettings.port.number"},
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

// externalAlias is like backendAlias, but for the out-of-mesh backend (ServiceEntry DNS to the
// external Service), so the backend observes exactly what the gateway sends.
func externalAlias(trafficPolicy string) string {
	return externalServiceEntry(80, "HTTP") + `---
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

// externalPolicyGateway is like policyGateway but routes to the out-of-mesh backend. Use it for
// policies that would also be enforced by an in-mesh backend's sidecar (namespace-wide or mesh-wide
// AuthorizationPolicy), so that only the gateway can produce the observed result.
func externalPolicyGateway(t framework.TestContext, s *Scenario) *Gateway {
	gw := s.NewGateway(t, GatewayOptions{})
	s.Apply(t, externalRoute(gw), map[string]any{"Name": s.Name("ext")})
	gw.Expect(t, Request{}, FromService(ExternalSvc))
	return gw
}

// expectBaselineHeader requires that a default gateway of the proxy under test sets the header
// (on the request to the backend, or on the response). "Disabled" features use it so they cannot
// pass merely because the proxy never sets the header.
func expectBaselineHeader(t framework.TestContext, s *Scenario, header string, response bool) {
	t.Helper()
	gw := proxyConfigGateway(t, s, `{}`, true, "baseline")
	if response {
		gw.Expect(t, Request{}, ResponseHeaderPresent(header))
	} else {
		gw.Expect(t, Request{}, RequestHeaderPresent(header))
	}
}

// requestsTotal sums all istio_requests_total samples exported by the gateway.
// requestsTotalIn sums istio_requests_total in a Prometheus scrape.
func requestsTotalIn(m string) float64 {
	var sum float64
	for _, l := range strings.Split(m, "\n") {
		if !strings.HasPrefix(l, "istio_requests_total{") {
			continue
		}
		f := strings.Fields(l)
		var v float64
		if _, err := fmt.Sscanf(f[len(f)-1], "%g", &v); err == nil {
			sum += v
		}
	}
	return sum
}
