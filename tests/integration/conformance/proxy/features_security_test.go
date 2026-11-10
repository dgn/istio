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
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo/check"
	"istio.io/istio/tests/common/jwt"
)

// Security features. Every one of these must either be enforced, or visibly rejected: a policy that
// is accepted but silently not enforced is the worst possible outcome (see design doc §4.3).
// Role-neutral features attach their policies through a Target (.PolicyAttachment): targetRefs to the
// gateway on ingress, a selector on the server workload for sidecars, targetRefs to the backend
// Service for waypoints. Features about gateway-specific attachment or client addresses stay
// ingress-only.
func init() {
	Register(
		Feature{
			ID:          "security.authz.deny.path",
			Description: "AuthorizationPolicy DENY on paths rejects matching requests with 403.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"AuthorizationPolicy.targetRefs", "AuthorizationPolicy.action=DENY", "AuthorizationPolicy.rules.to.operation.paths"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.AuthorizationPolicy("DENY", `
  - to:
    - operation:
        paths: ["/deny*"]
`))
				tg.Expect(t, Request{Path: "/deny/x"}, check.Status(403))
				tg.Expect(t, Request{Path: "/allow"}, check.OK())
			},
		},
		Feature{
			ID:          "security.authz.deny.method-header",
			Description: "AuthorizationPolicy DENY on methods and request headers (when) rejects matching requests.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"AuthorizationPolicy.rules.to.operation.methods", "AuthorizationPolicy.rules.when.key", "AuthorizationPolicy.rules.when.values"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.AuthorizationPolicy("DENY", `
  - to:
    - operation:
        methods: ["DELETE"]
  - when:
    - key: request.headers[x-block]
      values: ["yes"]
`))
				// Methods and header conditions are matched independently; an implementation may
				// support one and not the other, which is Partial.
				s.Case(t, "methods", func(t framework.TestContext) {
					tg.Expect(t, Request{Method: "DELETE"}, check.Status(403))
				})
				s.Case(t, "when-header", func(t framework.TestContext) {
					tg.Expect(t, Request{Headers: H("x-block", "yes")}, check.Status(403))
				})
				tg.Expect(t, Request{Headers: H("x-block", "no")}, check.OK())
			},
		},
		Feature{
			ID:          "security.authz.deny.host",
			Description: "AuthorizationPolicy DENY on hosts rejects requests for the matching Host only.",
			Roles:       ingressRole,
			Covers:      []string{"AuthorizationPolicy.rules.to.operation.hosts"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := policyGateway(t, s, "denied")
				s.Apply(t, authzPolicy(gw, "DENY", `
  - to:
    - operation:
        hosts: ["denied.{{.Host}}"]
`))
				gw.Expect(t, Request{Host: "denied." + s.Host()}, check.Status(403))
				gw.Expect(t, Request{}, check.OK())
			},
		},
		Feature{
			ID:          "security.authz.allow.header",
			Description: "AuthorizationPolicy ALLOW only permits matching requests; everything else is denied with 403.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"AuthorizationPolicy.action=ALLOW"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.AuthorizationPolicy("ALLOW", `
  - to:
    - operation:
        paths: ["/public*"]
  - when:
    - key: request.headers[x-allow]
      values: ["yes"]
`))
				tg.Expect(t, Request{Path: "/private"}, check.Status(403))
				tg.Expect(t, Request{Path: "/public/x"}, check.OK())
				tg.Expect(t, Request{Path: "/private", Headers: H("x-allow", "yes")}, check.OK())
			},
		},
		Feature{
			ID:          "security.authz.allow-nothing",
			Description: "An ALLOW AuthorizationPolicy with no rules denies all traffic to the proxy under test.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"AuthorizationPolicy.rules"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, `
apiVersion: security.istio.io/v1
kind: AuthorizationPolicy
metadata:
  name: {{.Name}}
spec:
{{.PolicyAttachment}}
`)
				tg.Expect(t, Request{}, check.Status(403))
			},
		},
		Feature{
			ID:          "security.authz.deny-overrides-allow",
			Description: "When both ALLOW and DENY policies match, DENY wins.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.AuthorizationPolicy("ALLOW", `
  - to:
    - operation:
        paths: ["/*"]
`, "allow"))
				s.Apply(t, tg.AuthorizationPolicy("DENY", `
  - to:
    - operation:
        paths: ["/admin*"]
`, "deny"))
				tg.Expect(t, Request{Path: "/admin"}, check.Status(403))
				tg.Expect(t, Request{Path: "/user"}, check.OK())
			},
		},
		Feature{
			ID:          "security.authz.selector",
			Description: "AuthorizationPolicy attached by workload selector (the gateway pods, or the server workload of a sidecar) is enforced.",
			Roles:       []Role{RoleIngress, RoleSidecar},
			RequiresL7:  true,
			Covers:      []string{"AuthorizationPolicy.selector", "AuthorizationPolicy.selector.matchLabels"},
			// Not waypoint: selector policies never bind to a waypoint (ztunnel enforces them, at L4).
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, `
apiVersion: security.istio.io/v1
kind: AuthorizationPolicy
metadata:
  name: {{.Name}}
spec:
  selector:
    matchLabels:
      {{.WorkloadSelector}}
  action: DENY
  rules:
  - to:
    - operation:
        paths: ["/deny"]
`)
				tg.Expect(t, Request{Path: "/deny"}, check.Status(403))
				tg.Expect(t, Request{Path: "/ok"}, check.OK())
			},
		},
		Feature{
			ID:          "security.authz.remote-ip",
			Description: "AuthorizationPolicy DENY on remoteIpBlocks rejects requests from the client's IP.",
			Roles:       ingressRole,
			Covers:      []string{"AuthorizationPolicy.rules.from.source.remoteIpBlocks"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := policyGateway(t, s)
				s.Apply(t, authzPolicy(gw, "DENY", `
  - from:
    - source:
        remoteIpBlocks: ["{{.ClientIP}}/32"]
    to:
    - operation:
        paths: ["/deny"]
`), map[string]any{"ClientIP": clientIP(t, s)})
				pod := gw.PodIP(t)
				gw.Expect(t, Request{Address: pod, Path: "/deny"}, check.Status(403))
				gw.Expect(t, Request{Address: pod, Path: "/ok"}, check.OK())
			},
		},
		Feature{
			ID:          "security.authz.ip-blocks-l4",
			Description: "AuthorizationPolicy DENY on ipBlocks (peer address, L4) rejects connections from the client's IP, including TCP.",
			Roles:       ingressRole,
			Covers:      []string{"AuthorizationPolicy.rules.from.source.ipBlocks"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := policyGateway(t, s)
				s.Apply(t, authzPolicy(gw, "DENY", `
  - from:
    - source:
        ipBlocks: ["{{.ClientIP}}/32"]
`), map[string]any{"ClientIP": clientIP(t, s)})
				gw.Expect(t, Request{Address: gw.PodIP(t)}, check.Status(403))
			},
		},
		Feature{
			ID:          "security.authz.not-values",
			Description: "AuthorizationPolicy negative matches (notPaths, notMethods) are honored.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"AuthorizationPolicy.rules.to.operation.notPaths", "AuthorizationPolicy.rules.to.operation.notMethods"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.AuthorizationPolicy("DENY", `
  - to:
    - operation:
        notPaths: ["/healthz"]
        notMethods: ["HEAD"]
`))
				tg.Expect(t, Request{Path: "/other"}, check.Status(403))
				tg.Expect(t, Request{Path: "/healthz"}, check.OK())
			},
		},
		Feature{
			ID:          "security.authz.custom-ext-authz",
			Description: "AuthorizationPolicy CUSTOM delegates the decision to an ext-authz (HTTP) provider from MeshConfig.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"AuthorizationPolicy.action=CUSTOM", "AuthorizationPolicy.provider", "AuthorizationPolicy.provider.name"},
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
  provider:
    name: ext-authz-http
  rules:
  - to:
    - operation:
        paths: ["/custom*"]
`)
				// The ext-authz test server allows requests carrying "x-ext-authz: allow".
				tg.Expect(t, Request{Path: "/custom"}, check.Status(403))
				tg.Expect(t, Request{Path: "/custom", Headers: H("x-ext-authz", "allow")}, check.OK())
				tg.Expect(t, Request{Path: "/other"}, check.OK())
			},
		},
		Feature{
			ID:          "security.authz.custom-ext-authz-grpc",
			Description: "AuthorizationPolicy CUSTOM with an ext-authz gRPC provider.",
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
  provider:
    name: ext-authz-grpc
  rules:
  - {}
`)
				tg.Expect(t, Request{}, check.Status(403))
				tg.Expect(t, Request{Headers: H("x-ext-authz", "allow")}, check.OK())
			},
		},
		Feature{
			ID:          "security.jwt.validation",
			Description: "RequestAuthentication rejects invalid or expired JWTs with 401 and accepts valid ones; requests without a token pass.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"RequestAuthentication.targetRefs", "RequestAuthentication.jwtRules", "RequestAuthentication.jwtRules.issuer", "RequestAuthentication.jwtRules.jwks"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.RequestAuthentication(""))
				tg.Expect(t, Request{Headers: bearer(jwt.TokenInvalid)}, check.Status(401))
				tg.Expect(t, Request{Headers: bearer(jwt.TokenExpired)}, check.Status(401))
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1)}, check.OK())
				tg.Expect(t, Request{}, check.OK())
			},
		},
		Feature{
			ID:          "security.jwt.require-principal",
			Description: "RequestAuthentication plus AuthorizationPolicy requestPrincipals requires a valid JWT (403 without).",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"AuthorizationPolicy.rules.from.source.requestPrincipals"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.RequestAuthentication(""))
				s.Apply(t, tg.AuthorizationPolicy("ALLOW", `
  - from:
    - source:
        requestPrincipals: ["test-issuer-1@istio.io/sub-1"]
`))
				tg.Expect(t, Request{}, check.Status(403))
				// Issuer 2 is not configured: the token is invalid (401), not merely unauthorized.
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer2)}, check.Status(401))
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1)}, check.OK())
			},
		},
		Feature{
			ID:          "security.jwt.claims-authz",
			Description: "AuthorizationPolicy conditions on request.auth.claims (including nested claims and groups) are enforced.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.RequestAuthentication(""))
				s.Apply(t, tg.AuthorizationPolicy("ALLOW", `
  - when:
    - key: request.auth.claims[groups]
      values: ["group-1"]
  - when:
    - key: request.auth.claims[nested][key1]
      values: ["valueA"]
`))
				// Valid token without the required claims.
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1WithAud)}, check.Status(403))
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1)}, check.OK())
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1WithNestedClaims1)}, check.OK())
			},
		},
		Feature{
			ID:          "security.jwt.audiences",
			Description: "RequestAuthentication audiences rejects tokens without a matching aud claim.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"RequestAuthentication.jwtRules.audiences"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.RequestAuthentication(`
    audiences: ["foo"]
`))
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1)}, Rejected())
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1WithAud)}, check.OK())
			},
		},
		Feature{
			ID:          "security.jwt.strip-token",
			Description: "By default the validated JWT is removed from the request forwarded to the backend.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.RequestAuthentication(""))
				tg.ExpectStable(t, Request{Headers: bearer(jwt.TokenIssuer1)}, RequestHeaderAbsent("Authorization"))
			},
		},
		Feature{
			ID:          "security.jwt.forward-original-token",
			Description: "forwardOriginalToken keeps the JWT on the request forwarded to the backend.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"RequestAuthentication.jwtRules.forwardOriginalToken"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.RequestAuthentication(`
    forwardOriginalToken: true
`))
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1)}, RequestHeaderContains("Authorization", "Bearer "))
				// Still validated:
				tg.Expect(t, Request{Headers: bearer(jwt.TokenInvalid)}, check.Status(401))
			},
		},
		Feature{
			ID:          "security.jwt.from-header-and-param",
			Description: "RequestAuthentication fromHeaders (with prefix) and fromParams extract the token from custom locations.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"RequestAuthentication.jwtRules.fromHeaders", "RequestAuthentication.jwtRules.fromHeaders.name", "RequestAuthentication.jwtRules.fromHeaders.prefix", "RequestAuthentication.jwtRules.fromParams"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.RequestAuthentication(`
    fromHeaders:
    - name: x-jwt
      prefix: "Token "
    fromParams: ["access_token"]
`))
				s.Apply(t, tg.AuthorizationPolicy("ALLOW", `
  - from:
    - source:
        requestPrincipals: ["*"]
`))
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1)}, check.Status(403))
				tg.Expect(t, Request{Headers: H("x-jwt", "Token "+jwt.TokenIssuer1)}, check.OK())
				tg.Expect(t, Request{Path: "/?access_token=" + jwt.TokenIssuer1}, check.OK())
			},
		},
		Feature{
			ID:          "security.jwt.output-claim-to-headers",
			Description: "RequestAuthentication outputClaimToHeaders copies claims into request headers seen by the backend.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"RequestAuthentication.jwtRules.outputClaimToHeaders", "RequestAuthentication.jwtRules.outputClaimToHeaders.header", "RequestAuthentication.jwtRules.outputClaimToHeaders.claim"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.RequestAuthentication(`
    outputClaimToHeaders:
    - header: x-jwt-sub
      claim: sub
    - header: x-jwt-nested
      claim: nested.key2
`))
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1)}, check.RequestHeader("x-jwt-sub", "sub-1"))
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1WithNestedClaims2)}, check.RequestHeader("x-jwt-nested", "valueC"))
			},
		},
		Feature{
			ID:          "security.jwt.output-payload-to-header",
			Description: "RequestAuthentication outputPayloadToHeader forwards the base64 JWT payload in a header.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"RequestAuthentication.jwtRules.outputPayloadToHeader"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.RequestAuthentication(`
    outputPayloadToHeader: x-jwt-payload
`))
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1)}, RequestHeaderPresent("x-jwt-payload"))
			},
		},
		Feature{
			ID:          "security.jwt.multiple-issuers",
			Description: "RequestAuthentication with several jwtRules accepts tokens from any configured issuer.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, `
apiVersion: security.istio.io/v1
kind: RequestAuthentication
metadata:
  name: {{.Name}}
spec:
{{.PolicyAttachment}}
  jwtRules:
  - issuer: test-issuer-1@istio.io
    jwks: '{{.JWKS}}'
  - issuer: test-issuer-2@istio.io
    jwks: '{{.JWKS}}'
`, map[string]any{"JWKS": jwt.JwksJSON})
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer1)}, check.OK())
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer2)}, check.OK())
				tg.Expect(t, Request{Headers: bearer(jwt.TokenIssuer3)}, check.Status(401))
			},
		},
	)
}

// policyGateway creates a dedicated gateway (policies are gateway-wide) with an HTTPRoute for
// Host() (and "<prefix>.Host()" for each prefix) to the v1 backend.
func policyGateway(t framework.TestContext, s *Scenario, prefixes ...string) *Gateway {
	gw := s.NewGateway(t, GatewayOptions{})
	hosts := []string{s.Host()}
	for _, p := range prefixes {
		hosts = append(hosts, p+"."+s.Host())
	}
	s.Apply(t, `
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}-route
spec:
  parentRefs:
  - name: {{.GW}}
  hostnames:
{{- range .Hosts }}
  - "{{ . }}"
{{- end }}
  rules:
  - backendRefs:
    - {name: {{.BackendV1}}, port: 80}
`, map[string]any{"GW": gw.Name, "Hosts": hosts})
	// Make sure the route works before any policy is applied, so later failures are attributable.
	gw.Expect(t, Request{}, check.OK())
	return gw
}

// authzPolicy returns an AuthorizationPolicy template targeting gw with the given action and
// rules (YAML list items indented for spec.rules).
func authzPolicy(gw *Gateway, action, rules string, suffix ...string) string {
	name := "{{.Name}}"
	if len(suffix) > 0 {
		name += "-" + suffix[0]
	}
	return `
apiVersion: security.istio.io/v1
kind: AuthorizationPolicy
metadata:
  name: ` + name + `
spec:
  targetRefs:
  - {kind: Gateway, group: gateway.networking.k8s.io, name: ` + gw.Name + `}
  action: ` + action + `
  rules:
` + rules
}

// requestAuthn returns a RequestAuthentication template for issuer 1 (inline JWKS) targeting gw.
// extra is appended to the jwtRule (indented 4 spaces).
func requestAuthn(gw *Gateway, extra string) string {
	return `
apiVersion: security.istio.io/v1
kind: RequestAuthentication
metadata:
  name: {{.Name}}-jwt
spec:
  targetRefs:
  - {kind: Gateway, group: gateway.networking.k8s.io, name: ` + gw.Name + `}
  jwtRules:
  - issuer: test-issuer-1@istio.io
    jwks: '` + jwt.JwksJSON + `'
` + extra
}

func bearer(token string) map[string][]string {
	return H("Authorization", "Bearer "+token)
}

func clientIP(t framework.TestContext, s *Scenario) string {
	return s.Env.Client.WorkloadsOrFail(t)[0].Address()
}
