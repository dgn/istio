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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	krand "k8s.io/apimachinery/pkg/util/rand"

	"istio.io/istio/pkg/test"
	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/util/retry"
	"istio.io/istio/tests/common/jwt"
)

// Security at the gateway beyond the role-neutral policies of features_security_test.go: policy
// attachment with the deprecated singular targetRef, peer identity from client certificates (an
// ingress gateway sees a SPIFFE identity only from an mTLS client, so these use a MUTUAL listener and
// certificates with SPIFFE URI SANs), policies combined across scopes, remotely fetched JWKS, and the
// X-Forwarded-Client-Cert modes of gatewayTopology.
func init() {
	Register(
		// --- Attachment ---
		Feature{
			ID:          "security.authz.target-ref",
			Description: "An AuthorizationPolicy attached with the deprecated singular targetRef applies to the gateway.",
			Roles:       ingressRole,
			Covers: []string{
				"AuthorizationPolicy.targetRef", "AuthorizationPolicy.targetRef.group", "AuthorizationPolicy.targetRef.kind",
				"AuthorizationPolicy.targetRef.name",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := policyGateway(t, s)
				s.Apply(t, `
apiVersion: security.istio.io/v1
kind: AuthorizationPolicy
metadata:
  name: {{.Name}}
spec:
  targetRef: {kind: Gateway, group: gateway.networking.k8s.io, name: {{.GW}}}
  action: DENY
  rules:
  - to:
    - operation:
        paths: ["/denied"]
`, map[string]any{"GW": gw.Name})
				gw.Expect(t, Request{Path: "/denied"}, check.Status(403))
				gw.Expect(t, Request{Path: "/allowed"}, check.OK())
			},
		},
		Feature{
			ID: "security.jwt.target-ref",
			Description: "RequestAuthentications attached with the deprecated singular targetRef and with targetRefs (group, kind, name) " +
				"both apply to the gateway: tokens of either issuer are accepted, invalid ones rejected.",
			Roles: ingressRole,
			Covers: []string{
				"RequestAuthentication.targetRef", "RequestAuthentication.targetRef.group", "RequestAuthentication.targetRef.kind",
				"RequestAuthentication.targetRef.name", "RequestAuthentication.targetRefs.group", "RequestAuthentication.targetRefs.kind",
				"RequestAuthentication.targetRefs.name",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := policyGateway(t, s)
				// A token of an issuer no attached policy knows is rejected, so each issuer's token is
				// accepted only if its own policy is attached.
				s.Apply(t, `
apiVersion: security.istio.io/v1
kind: RequestAuthentication
metadata:
  name: {{.Name}}-singular
spec:
  targetRef: {kind: Gateway, group: gateway.networking.k8s.io, name: {{.GW}}}
  jwtRules:
  - issuer: test-issuer-1@istio.io
    jwks: '{{.JWKS}}'
---
apiVersion: security.istio.io/v1
kind: RequestAuthentication
metadata:
  name: {{.Name}}-list
spec:
  targetRefs:
  - {kind: Gateway, group: gateway.networking.k8s.io, name: {{.GW}}}
  jwtRules:
  - issuer: test-issuer-2@istio.io
    jwks: '{{.JWKS}}'
`, map[string]any{"GW": gw.Name, "JWKS": jwt.JwksJSON})
				gw.Expect(t, Request{Headers: bearer(jwt.TokenInvalid)}, check.Status(401))
				gw.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1)}, check.OK())
				gw.Expect(t, Request{Headers: bearer(jwt.TokenIssuer2)}, check.OK())
				gw.Expect(t, Request{Headers: bearer(jwt.TokenIssuer3)}, check.Status(401))
			},
		},
		Feature{
			ID: "telemetry.target-ref",
			Description: "Telemetry attached to a gateway with the deprecated singular targetRef, or with targetRefs (group, kind, name), " +
				"applies to that gateway.",
			Roles: ingressRole,
			Covers: []string{
				"Telemetry.targetRef", "Telemetry.targetRef.group", "Telemetry.targetRef.kind", "Telemetry.targetRef.name",
				"Telemetry.targetRefs.group", "Telemetry.targetRefs.kind", "Telemetry.targetRefs.name",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				// Only one workload-scoped Telemetry applies per proxy, so each form gets its own gateway.
				for _, form := range []struct{ name, ref string }{
					{"singular", `
  targetRef: {kind: Gateway, group: gateway.networking.k8s.io, name: {{.GW}}}`},
					{"list", `
  targetRefs:
  - {kind: Gateway, group: gateway.networking.k8s.io, name: {{.GW}}}`},
				} {
					gw := s.NewGateway(t, GatewayOptions{Suffix: form.name})
					s.Apply(t, httpRouteTo(gw, "{{.BackendV1}}", 80), map[string]any{"Name": s.Name(form.name)})
					nonce := krand.String(8)
					s.Apply(t, `
apiVersion: telemetry.istio.io/v1
kind: Telemetry
metadata:
  name: {{.Name}}-{{.Form}}
spec:`+form.ref+`
`+WitnessMetrics(nonce), map[string]any{"GW": gw.Name, "Form": form.name})
					gw.WitnessTelemetry(t, nonce)
				}
			},
		},

		// --- Peer identity from client certificates ---
		Feature{
			ID: "security.authz.source-trust-domains",
			Description: "AuthorizationPolicy source.trustDomains, notTrustDomains and notServiceAccounts match the SPIFFE identity " +
				"of an mTLS client certificate at the gateway.",
			Roles: ingressRole,
			Covers: []string{
				"AuthorizationPolicy.rules.from", "AuthorizationPolicy.rules.from.source", "AuthorizationPolicy.rules.from.source.trustDomains",
				"AuthorizationPolicy.rules.from.source.notTrustDomains", "AuthorizationPolicy.rules.from.source.notServiceAccounts",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				ca := newCA(t)
				ig := newIdentityGateway(t, s, ca, GatewayOptions{})
				ns := s.Namespace()
				a := ca.spiffeLeaf(t, "spiffe://pcs-td-a.example/ns/"+ns+"/sa/pcs-a")
				b := ca.spiffeLeaf(t, "spiffe://pcs-td-b.example/ns/"+ns+"/sa/pcs-b")
				s.Apply(t, authzPolicy(ig.Gateway, "DENY", `
  - from: [{source: {trustDomains: [pcs-td-a.example]}}]
    to: [{operation: {paths: [/td]}}]
  - from: [{source: {notTrustDomains: [pcs-td-a.example]}}]
    to: [{operation: {paths: [/not-td]}}]
  - from: [{source: {notServiceAccounts: ["{{.Namespace}}/pcs-a"]}}]
    to: [{operation: {paths: [/not-sa]}}]
`))
				// The first refusal witnesses that the policy is in effect; every rule then has an accepted
				// and a refused identity on the same path.
				ig.Expect(t, a, "/td", check.Status(403))
				ig.Expect(t, b, "/td", check.OK())
				ig.Expect(t, a, "/not-td", check.OK())
				ig.Expect(t, b, "/not-td", check.Status(403))
				ig.Expect(t, a, "/not-sa", check.OK())
				ig.Expect(t, b, "/not-sa", check.Status(403))
			},
		},
		Feature{
			ID: "meshconfig.trust-domain-aliases",
			Description: "With meshConfig.trustDomainAliases, an AuthorizationPolicy principal in the mesh trust domain also matches " +
				"the same identity in an alias trust domain, and not in other trust domains.",
			Roles:      ingressRole,
			Covers:     []string{"MeshConfig.trustDomainAliases"},
			MeshConfig: "trustDomainAliases: [pcs-alias.example]",
			Run: func(t framework.TestContext, s *Scenario) {
				ca := newCA(t)
				ig := newIdentityGateway(t, s, ca, GatewayOptions{})
				id := "/ns/" + s.Namespace() + "/sa/pcs-alias"
				native := ca.spiffeLeaf(t, "spiffe://cluster.local"+id)
				alias := ca.spiffeLeaf(t, "spiffe://pcs-alias.example"+id)
				other := ca.spiffeLeaf(t, "spiffe://pcs-other.example"+id)
				// The policy names the identity in the installed trust domain (cluster.local); istiod expands
				// it to every trust domain of the bundle (the mesh trust domain plus its aliases).
				s.Apply(t, authzPolicy(ig.Gateway, "ALLOW", `
  - from: [{source: {principals: ["cluster.local{{.ID}}"]}}]
`), map[string]any{"ID": id})
				ig.Expect(t, other, "/", check.Status(403))
				ig.Expect(t, native, "/", check.OK())
				ig.Expect(t, alias, "/", check.OK())
			},
		},

		// --- Scopes ---
		Feature{
			ID: "security.authz.scope-union",
			Description: "AuthorizationPolicies from the root namespace, the gateway's namespace and a targetRef all apply together: " +
				"a narrower ALLOW does not override a wider DENY.",
			Roles:  ingressRole,
			Covers: []string{"interaction:authz.scope-union"},
			Run: func(t framework.TestContext, s *Scenario) {
				// Out-of-mesh backend: an in-mesh backend's sidecar would enforce the namespace and root
				// policies itself.
				gw := externalPolicyGateway(t, s)
				root := istio.ClaimSystemNamespaceOrFail(t, t).Name()
				p := "/pcs-union-" + s.Name()
				// Wider scopes only DENY paths unique to this feature, so other features are unaffected.
				deny := `
apiVersion: security.istio.io/v1
kind: AuthorizationPolicy
metadata:
  name: {{.Name}}-{{.Scope}}
spec:
  action: DENY
  rules:
  - to: [{operation: {paths: ["{{.P}}/{{.Scope}}"]}}]
`
				s.ApplyTo(t, root, deny, map[string]any{"P": p, "Scope": "root"})
				s.Apply(t, deny, map[string]any{"P": p, "Scope": "ns"})
				s.Apply(t, authzPolicy(gw, "ALLOW", `
  - to: [{operation: {paths: ["{{.P}}/*"]}}]
`, "allow"), map[string]any{"P": p})
				s.Apply(t, authzPolicy(gw, "DENY", `
  - to: [{operation: {paths: ["{{.P}}/gw"]}}]
`, "deny"), map[string]any{"P": p})
				gw.Expect(t, Request{Path: "/other"}, check.Status(403))
				gw.Expect(t, Request{Path: p + "/ok"}, check.OK())
				gw.Expect(t, Request{Path: p + "/root"}, check.Status(403))
				gw.Expect(t, Request{Path: p + "/ns"}, check.Status(403))
				gw.Expect(t, Request{Path: p + "/gw"}, check.Status(403))
			},
		},

		// --- JWT ---
		Feature{
			ID: "security.jwt.presence-optional",
			Description: "RequestAuthentication jwtRules presence OPTIONAL accepts requests without that token but still rejects an " +
				"invalid one, alongside a REQUIRED rule.",
			Roles:      l7Roles,
			RequiresL7: true,
			Covers:     []string{"RequestAuthentication.jwtRules.presence=OPTIONAL"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.RequestAuthentication(`
    presence: REQUIRED
  - issuer: test-issuer-2@istio.io
    jwks: '`+jwt.JwksJSON+`'
    fromHeaders: [{name: x-optional-jwt}]
    presence: OPTIONAL
`))
				tg.Expect(t, Request{}, check.Status(401))
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1)}, check.OK())
				tg.Expect(t, Request{Headers: H("Authorization", "Bearer "+jwt.TokenIssuer1, "x-optional-jwt", jwt.TokenIssuer2)}, check.OK())
				tg.Expect(t, Request{Headers: H("Authorization", "Bearer "+jwt.TokenIssuer1, "x-optional-jwt", jwt.TokenInvalid)}, check.Status(401))
			},
		},
		Feature{
			ID:          "security.jwt.jwks-uri",
			Description: "RequestAuthentication jwtRules jwksUri: keys fetched from the URI validate tokens.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"RequestAuthentication.jwtRules.jwksUri"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				srv := newJWKSServer(t, s)
				s.Apply(t, `
apiVersion: security.istio.io/v1
kind: RequestAuthentication
metadata:
  name: {{.Name}}-jwt
spec:
{{.PolicyAttachment}}
  jwtRules:
  - issuer: test-issuer-1@istio.io
    jwksUri: {{.URI}}
`, map[string]any{"URI": srv.uri("fast")})
				tg.Expect(t, Request{Headers: bearer(jwt.TokenInvalid)}, check.Status(401))
				// Accepted only with the fetched keys: when the fetch fails, the token is rejected.
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1)}, check.OK())
			},
		},
		Feature{
			ID: "security.jwt.jwks-timeout",
			Description: "RequestAuthentication jwtRules timeout bounds the JWKS fetch: a slow JWKS server is used within a long timeout, " +
				"and tokens are rejected when the timeout is shorter than the server's delay.",
			Roles:      l7Roles,
			RequiresL7: true,
			Covers:     []string{"RequestAuthentication.jwtRules.timeout"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				srv := newJWKSServer(t, s)
				// Both URIs answer after jwksSlowDelay.
				s.Apply(t, `
apiVersion: security.istio.io/v1
kind: RequestAuthentication
metadata:
  name: {{.Name}}-jwt
spec:
{{.PolicyAttachment}}
  jwtRules:
  - issuer: test-issuer-1@istio.io
    jwksUri: {{.Long}}
    timeout: 20s
  - issuer: test-issuer-2@istio.io
    jwksUri: {{.Short}}
    timeout: 500ms
`, map[string]any{"Long": srv.uri("slow/long"), "Short": srv.uri("slow/short")})
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1)}, check.OK())
				tg.ExpectStable(t, Request{Headers: bearer(jwt.TokenIssuer2)}, check.Status(401))
			},
		},
		Feature{
			ID: "flag.pilot-jwt-enable-remote-jwks",
			Description: "With PILOT_JWT_ENABLE_REMOTE_JWKS=envoy the proxy fetches a jwksUri served by a mesh service itself, so a " +
				"JWKS host only the mesh can resolve (a ServiceEntry) works.",
			Roles:     ingressRole,
			Covers:    []string{"flag:istiod:PILOT_JWT_ENABLE_REMOTE_JWKS"},
			IstiodEnv: map[string]string{"PILOT_JWT_ENABLE_REMOTE_JWKS": "envoy"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := policyGateway(t, s)
				srv := newJWKSServer(t, s)
				// The ServiceEntry host has no DNS record, so istiod cannot fetch it (it would then inline
				// keys nobody can sign with, rejecting every token); the proxy reaches it through its cluster.
				host := s.Name() + ".jwks.pcs.example"
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}-jwks
spec:
  hosts: ["{{.SEHost}}"]
  location: MESH_EXTERNAL
  resolution: DNS
  ports:
  - {number: 8000, name: http, protocol: HTTP}
  endpoints:
  - address: {{.Svc}}
---
apiVersion: security.istio.io/v1
kind: RequestAuthentication
metadata:
  name: {{.Name}}-jwt
spec:
  targetRefs:
  - {kind: Gateway, group: gateway.networking.k8s.io, name: {{.GW}}}
  jwtRules:
  - issuer: test-issuer-1@istio.io
    jwksUri: http://{{.SEHost}}:8000/{{.Path}}
`, map[string]any{"SEHost": host, "Svc": srv.host, "GW": gw.Name, "Path": srv.path("fast")})
				gw.Expect(t, Request{Headers: bearer(jwt.TokenInvalid)}, check.Status(401))
				gw.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1)}, check.OK())
			},
		},

		// --- Mesh trust ---
		Feature{
			ID: "meshconfig.trust-domain",
			Description: "With meshConfig.trustDomain changed, an AuthorizationPolicy principal in cluster.local (which stands for the mesh " +
				"trust domain) matches the identity in the new trust domain, and no longer the one in cluster.local.",
			Roles:      ingressRole,
			Covers:     []string{"MeshConfig.trustDomain"},
			MeshConfig: "trustDomain: " + pcsTrustDomain,
			Run: func(t framework.TestContext, s *Scenario) {
				ca := newCA(t)
				ig := newIdentityGateway(t, s, ca, GatewayOptions{})
				id := "/ns/" + s.Namespace() + "/sa/pcs-td"
				s.Apply(t, authzPolicy(ig.Gateway, "ALLOW", `
  - from: [{source: {principals: ["cluster.local{{.ID}}"]}}]
`), map[string]any{"ID": id})
				// The first refusal witnesses the policy; under the installed trust domain the identity in
				// pcs-td.example is refused too and the one in cluster.local accepted.
				ig.Expect(t, ca.spiffeLeaf(t, "spiffe://pcs-other.example"+id), "/", check.Status(403))
				ig.Expect(t, ca.spiffeLeaf(t, "spiffe://"+pcsTrustDomain+id), "/", check.OK())
				ig.Expect(t, ca.spiffeLeaf(t, "spiffe://cluster.local"+id), "/", check.Status(403))
			},
		},
		Feature{
			ID: "meshconfig.ca-certificates",
			Description: "meshConfig.caCertificates pem adds a trust anchor for mesh mTLS (ISTIO_MULTIROOT_MESH, with the proxy taking " +
				"the bundle over PROXY_CONFIG_XDS_AGENT): the gateway verifies an ISTIO_MUTUAL backend whose certificate chains to it, " +
				"and still checks its subjectAltNames.",
			Roles: ingressRole,
			Covers: []string{
				"MeshConfig.caCertificates", "MeshConfig.caCertificates.pem", "flag:istiod:ISTIO_MULTIROOT_MESH",
				"flag:agent:PROXY_CONFIG_XDS_AGENT",
			},
			IstiodEnv:  map[string]string{"ISTIO_MULTIROOT_MESH": "true"},
			MeshConfig: caCertificatesMeshConfig(),
			Run: func(t framework.TestContext, s *Scenario) {
				// The agent takes the mesh trust bundle (PCDS) only with PROXY_CONFIG_XDS_AGENT, which the
				// gateway deployment gets from a ProxyConfig selecting its pods; it must exist first.
				s.Apply(t, `
apiVersion: networking.istio.io/v1beta1
kind: ProxyConfig
metadata:
  name: {{.Name}}-xds
spec:
  selector:
    matchLabels:
      gateway.networking.k8s.io/gateway-name: {{.GW}}
  environmentVariables:
    PROXY_CONFIG_XDS_AGENT: "true"
`, map[string]any{"GW": s.Name("gw", "")})
				gw := externalPolicyGateway(t, s)
				// The external backend's TLS port serves a certificate from the echo test CA, which only the
				// added trust anchor makes valid for ISTIO_MUTUAL (validated against the mesh roots).
				for _, c := range []struct{ label, san string }{{"good", BackendTLSHost}, {"bad", "wrong.example.com"}} {
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
  # HTTP2 adds h2 to the ISTIO_MUTUAL ALPN, which the echo TLS server requires.
  - {number: 80, name: http2, protocol: HTTP2, targetPort: 443}
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
      mode: ISTIO_MUTUAL
      sni: {{.TLSHost}}
      subjectAltNames: ["{{.SAN}}"]
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
`, map[string]any{
						"Name": s.Name(c.label), "GW": gw.Name, "SEHost": c.label + "." + s.Name() + "." + ExternalHost,
						"Host": c.label + "." + s.Host(), "TLSHost": BackendTLSHost, "SAN": c.san,
					})
				}
				gw.Expect(t, Request{Host: "good." + s.Host()}, FromService(ExternalSvc))
				gw.ExpectStable(t, Request{Host: "bad." + s.Host()}, check.Status(503))
			},
		},
		Feature{
			ID: "security.mtls.gateway-to-restricted-sidecar",
			Description: "A gateway still reaches a sidecar backend over mesh mTLS when meshConfig.meshMTLS restricts the sidecar's " +
				"inbound to TLS 1.3 and a single TLS 1.2 cipher suite.",
			Roles:      ingressRole,
			Covers:     []string{"interaction:mtls.gateway-to-restricted-sidecar"},
			MeshConfig: "meshMTLS: {minProtocolVersion: TLSV1_3, cipherSuites: [ECDHE-ECDSA-AES256-GCM-SHA384]}",
			NoControl: "meshMTLS applies only to the server side of mesh mTLS (pilot BuildInboundTLS); a gateway's ISTIO_MUTUAL " +
				"upstream always offers TLS 1.3 (defaultUpstreamCommonTLSContext), where cipherSuites do not apply, so no gateway " +
				"request is refused with or without it and the echo client cannot cap its TLS version to probe the sidecar itself.",
			Run: func(t framework.TestContext, s *Scenario) {
				// A dedicated gateway: its upstream connections to the backend's sidecar are opened after the
				// restriction, where the shared gateway's could predate it.
				tg := s.Target(t, TargetOptions{})
				// X-Forwarded-Client-Cert is set by the backend's sidecar only for mesh mTLS, so the request
				// went through the restricted inbound chain rather than the permissive plaintext one.
				tg.ExpectStable(t, Request{Opts: newConnPerRequest}, check.And(check.OK(), RequestHeaderContains("X-Forwarded-Client-Cert", "By=spiffe://")))
			},
		},

		// --- X-Forwarded-Client-Cert ---
		Feature{
			ID:          "dataplane.xfcc.mode-sanitize",
			Description: "gatewayTopology.forwardClientCertDetails SANITIZE drops X-Forwarded-Client-Cert.",
			Roles:       ingressRole,
			Covers:      []string{"ProxyConfig.gatewayTopology.forwardClientCertDetails=SANITIZE"},
			Run:         xfccRun("SANITIZE", check.And(check.OK(), RequestHeaderAbsent("X-Forwarded-Client-Cert"))),
		},
		Feature{
			ID:          "dataplane.xfcc.mode-forward-only",
			Description: "gatewayTopology.forwardClientCertDetails FORWARD_ONLY forwards the client's X-Forwarded-Client-Cert unchanged on mTLS connections.",
			Roles:       ingressRole,
			Covers:      []string{"ProxyConfig.gatewayTopology.forwardClientCertDetails=FORWARD_ONLY"},
			Run: xfccRun("FORWARD_ONLY", check.And(check.OK(), RequestHeaderContains("X-Forwarded-Client-Cert", xfccUpstream),
				headerNotContains("X-Forwarded-Client-Cert", xfccClient))),
		},
		Feature{
			ID:          "dataplane.xfcc.mode-append-forward",
			Description: "gatewayTopology.forwardClientCertDetails APPEND_FORWARD appends the client certificate's details to the client's X-Forwarded-Client-Cert.",
			Roles:       ingressRole,
			Covers:      []string{"ProxyConfig.gatewayTopology.forwardClientCertDetails=APPEND_FORWARD"},
			Run: xfccRun("APPEND_FORWARD", check.And(check.OK(), RequestHeaderContains("X-Forwarded-Client-Cert", xfccUpstream),
				RequestHeaderContains("X-Forwarded-Client-Cert", xfccClient))),
		},
		Feature{
			ID:          "dataplane.xfcc.mode-sanitize-set",
			Description: "gatewayTopology.forwardClientCertDetails SANITIZE_SET replaces the client's X-Forwarded-Client-Cert with the client certificate's details.",
			Roles:       ingressRole,
			Covers:      []string{"ProxyConfig.gatewayTopology.forwardClientCertDetails=SANITIZE_SET"},
			NoControl: "SANITIZE_SET is the gateway default (pilot buildGatewayConnectionManager), so the gateway behaves the same " +
				"without the annotation; the client X-Forwarded-Client-Cert being replaced is checked instead.",
			Run: xfccRun("SANITIZE_SET", check.And(check.OK(), RequestHeaderContains("X-Forwarded-Client-Cert", xfccClient),
				headerNotContains("X-Forwarded-Client-Cert", xfccUpstream))),
		},
		Feature{
			ID:          "dataplane.xfcc.mode-undefined",
			Description: "gatewayTopology.forwardClientCertDetails UNDEFINED behaves like the gateway default, SANITIZE_SET.",
			Roles:       ingressRole,
			Covers:      []string{"ProxyConfig.gatewayTopology.forwardClientCertDetails=UNDEFINED"},
			NoControl: "SANITIZE_SET is the gateway default (pilot buildGatewayConnectionManager), so the gateway behaves the same " +
				"without the annotation; the client X-Forwarded-Client-Cert being replaced is checked instead.",
			Run: xfccRun("UNDEFINED", check.And(check.OK(), RequestHeaderContains("X-Forwarded-Client-Cert", xfccClient),
				headerNotContains("X-Forwarded-Client-Cert", xfccUpstream))),
		},
	)
}

const (
	// pcsTrustDomain is the mesh trust domain meshconfig.trust-domain switches to.
	pcsTrustDomain = "pcs-td.example"

	// xfccUpstream is in the X-Forwarded-Client-Cert the client sends; xfccClient is the URI SAN of the
	// client certificate, which the gateway adds when it sets the current client's details.
	xfccUpstream = "spiffe://pcs-upstream.example/ns/x/sa/y"
	xfccClient   = "spiffe://pcs-client.example/ns/x/sa/client"
)

// caCertificatesMeshConfig is the MeshConfig patch adding the echo test CA, which signs the external
// backend's TLS certificate, as a mesh trust anchor.
func caCertificatesMeshConfig() string {
	pem := strings.TrimSpace(echoTestCA())
	return "caCertificates:\n- pem: |\n    " + strings.ReplaceAll(pem, "\n", "\n    ") + "\n"
}

// xfccRun runs one ProxyConfig gatewayTopology.forwardClientCertDetails mode: a gateway annotated
// with the mode terminates mTLS and routes to the out-of-mesh backend (an in-mesh backend's sidecar
// would set X-Forwarded-Client-Cert itself); an mTLS client sends its own X-Forwarded-Client-Cert, and
// the backend's view of the header must satisfy want.
func xfccRun(mode string, want echo.Checker) func(framework.TestContext, *Scenario) {
	return func(t framework.TestContext, s *Scenario) {
		ca := newCA(t)
		ig := newIdentityGateway(t, s, ca, GatewayOptions{Annotations: map[string]string{
			"proxy.istio.io/config": `{"gatewayTopology":{"forwardClientCertDetails":"` + mode + `"}}`,
		}})
		client := ca.spiffeLeaf(t, xfccClient)
		ig.Expect(t, client, "/", want, H("X-Forwarded-Client-Cert", "By="+xfccUpstream+";Hash=0"))
	}
}

// identityGateway is a dedicated gateway whose HTTPS listener (port 443, Istio terminate mode MUTUAL)
// requires a client certificate signed by a test CA, routed to the out-of-mesh backend. The client's
// SPIFFE identity is the URI SAN of its certificate (see testCA.spiffeLeaf).
type identityGateway struct {
	*Gateway
	host     string
	serverCA string
}

// newIdentityGateway creates an identityGateway trusting ca, with the GatewayOptions (Listeners is
// set here), and checks that a client certificate signed by ca is accepted.
func newIdentityGateway(t framework.TestContext, s *Scenario, ca *testCA, o GatewayOptions) *identityGateway {
	t.Helper()
	host := s.Host()
	server := newCerts(t, host, host)
	s.TLSSecret(t, s.Name("cert"), Certs{CA: ca.pem, Cert: server.Cert, Key: server.Key})
	o.Listeners = `
  - name: https
    port: 443
    protocol: HTTPS
    hostname: "{{.Host}}"
    tls:
      mode: Terminate
      certificateRefs: [{name: {{.Cert}}}]
      options:
        gateway.istio.io/tls-terminate-mode: MUTUAL
`
	o.ListenerVars = map[string]any{"Cert": s.Name("cert")}
	gw := s.NewGateway(t, o)
	s.Apply(t, externalRoute(gw))
	ig := &identityGateway{Gateway: gw, host: host, serverCA: server.CA}
	ig.Expect(t, ca.spiffeLeaf(t, "spiffe://pcs-baseline.example/ns/x/sa/baseline"), "/", FromService(ExternalSvc))
	return ig
}

// Expect sends an HTTPS request for path presenting the client certificate c, on a new connection
// (authorization changes apply to new connections), and retries until want passes.
func (ig *identityGateway) Expect(t framework.TestContext, c Certs, path string, want echo.Checker, headers ...http.Header) {
	t.Helper()
	r := Request{Port: 443, Scheme: scheme.HTTPS, Path: path, Opts: func(o *echo.CallOptions) {
		tlsClient(ig.host, ig.serverCA)(o)
		o.TLS.Cert, o.TLS.Key = c.Cert, c.Key
		newConnPerRequest(o)
	}}
	if len(headers) > 0 {
		r.Headers = headers[0]
	}
	ig.Gateway.Expect(t, r, want)
}

// spiffeLeaf issues a client certificate whose only SAN is the SPIFFE URI, the identity an mTLS peer
// presents to Istio authorization (source.principal, trust domain, namespace, service account).
func (c *testCA) spiffeLeaf(t test.Failer, uri string) Certs {
	t.Helper()
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(c.next),
		Subject:      pkix.Name{CommonName: "pcs-spiffe-client"},
		URIs:         []*url.URL{u},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	c.next++
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return Certs{
		CA:   c.pem,
		Cert: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		Key:  string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})),
	}
}

// jwksSlowDelay is how long the JWKS server takes to answer paths under /slow/.
const jwksSlowDelay = 3 * time.Second

// jwksServer is a per-feature, out-of-mesh HTTP server answering every path with the JWKS of the test
// signing key (tests/common/jwt), after jwksSlowDelay for paths under /slow/. It is test
// infrastructure, not the proxy under test: a standalone Envoy (from the proxyv2 image, the only image
// with an HTTP server that can return a fixed body) with a static configuration and no Istio config.
type jwksServer struct {
	// host is the cluster-local hostname of its Service (port 8000).
	host string
	// nonce makes URIs unique per run: istiod caches fetched keys per URI.
	nonce string
}

// uri returns the JWKS URI for path (e.g. "fast" or "slow/x") on the Service.
func (j jwksServer) uri(path string) string {
	return "http://" + j.host + ":8000/" + j.path(path)
}

// path returns the per-run URI path for path.
func (j jwksServer) path(path string) string {
	return path + "/" + j.nonce
}

func newJWKSServer(t framework.TestContext, s *Scenario) jwksServer {
	t.Helper()
	direct := map[string]any{"status": 200, "body": map[string]any{"inline_string": jwt.JwksJSON}}
	bootstrap := map[string]any{
		"static_resources": map[string]any{
			"listeners": []any{map[string]any{
				"name":    "jwks",
				"address": map[string]any{"socket_address": map[string]any{"address": "0.0.0.0", "port_value": 8000}},
				"filter_chains": []any{map[string]any{"filters": []any{map[string]any{
					"name": "envoy.filters.network.http_connection_manager",
					"typed_config": map[string]any{
						"@type":       "type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager",
						"stat_prefix": "jwks",
						"route_config": map[string]any{"virtual_hosts": []any{map[string]any{
							"name":    "jwks",
							"domains": []string{"*"},
							"routes": []any{
								map[string]any{
									"match":           map[string]any{"prefix": "/slow/"},
									"direct_response": direct,
									"typed_per_filter_config": map[string]any{"envoy.filters.http.fault": map[string]any{
										"@type": "type.googleapis.com/envoy.extensions.filters.http.fault.v3.HTTPFault",
										"delay": map[string]any{
											"fixed_delay": jwksSlowDelay.String(),
											"percentage":  map[string]any{"numerator": 100, "denominator": "HUNDRED"},
										},
									}},
								},
								map[string]any{"match": map[string]any{"prefix": "/"}, "direct_response": direct},
							},
						}}},
						"http_filters": []any{
							map[string]any{
								"name":         "envoy.filters.http.fault",
								"typed_config": map[string]any{"@type": "type.googleapis.com/envoy.extensions.filters.http.fault.v3.HTTPFault"},
							},
							map[string]any{
								"name":         "envoy.filters.http.router",
								"typed_config": map[string]any{"@type": "type.googleapis.com/envoy.extensions.filters.http.router.v3.Router"},
							},
						},
					},
				}}}},
			}},
		},
	}
	cfg, err := json.Marshal(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	name := s.Name("jwks")
	img := t.Settings().Image
	s.Apply(t, `
apiVersion: v1
kind: Service
metadata:
  name: {{.App}}
spec:
  selector: {app: {{.App}}}
  ports:
  - {name: http, port: 8000, targetPort: 8000}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{.App}}
spec:
  selector:
    matchLabels: {app: {{.App}}}
  template:
    metadata:
      labels:
        app: {{.App}}
        sidecar.istio.io/inject: "false"
        istio.io/dataplane-mode: none
    spec:
      containers:
      - name: jwks
        image: {{.Image}}
        imagePullPolicy: {{.PullPolicy}}
        command: [/usr/local/bin/envoy]
        args: ["--disable-hot-restart", "--log-level", "warning", "--config-yaml", {{.Config | quote}}]
        ports:
        - containerPort: 8000
        readinessProbe:
          tcpSocket: {port: 8000}
          periodSeconds: 1
`, map[string]any{
		"App": name, "Image": img.Hub + "/proxyv2:" + img.Tag, "PullPolicy": img.PullPolicy, "Config": string(cfg),
	})
	ns := s.Namespace()
	retry.UntilSuccessOrFail(t, func() error {
		pods, err := t.Clusters().Default().Kube().CoreV1().Pods(ns).List(context.Background(), metav1.ListOptions{LabelSelector: "app=" + name})
		if err != nil {
			return err
		}
		for _, p := range pods.Items {
			if podReady(p) {
				return nil
			}
		}
		return fmt.Errorf("JWKS server %s not ready", name)
	}, retry.Timeout(3*time.Minute), retry.Delay(time.Second))
	return jwksServer{host: name + "." + ns + ".svc.cluster.local", nonce: krand.String(8)}
}
