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
)

// Generic dataplane behaviors: they must hold for any workload proxy, so they run against every
// dataplane flavor (sidecars, and ambient with a waypoint for RequiresL7 features). Traffic always
// goes client -> proxy under test -> server, with both ends in the same mode.
func init() {
	Register(
		Feature{
			ID:          "mesh.baseline.reachability",
			Description: "A meshed client reaches the backend through the proxy under test.",
			Roles:       []Role{RoleNode, RoleSidecar},
			NoControl:   "no config is applied; there is nothing to remove",
			Run: func(t framework.TestContext, s *Scenario) {
				s.Workload(t).Expect(t, Request{}, check.OK())
			},
		},
		Feature{
			ID:          "mesh.mtls.strict",
			Description: "With PeerAuthentication STRICT on the backend, meshed traffic still succeeds (mTLS is used end to end).",
			Roles:       []Role{RoleNode, RoleSidecar},
			Covers:      []string{"PeerAuthentication.selector", "PeerAuthentication.selector.matchLabels", "PeerAuthentication.mtls", "PeerAuthentication.mtls.mode=STRICT"},
			NoControl:   "STRICT is already the default for meshed traffic; removing it changes nothing observable",
			Run: func(t framework.TestContext, s *Scenario) {
				w := s.Workload(t)
				s.Apply(t, `
apiVersion: security.istio.io/v1
kind: PeerAuthentication
metadata:
  name: {{.Name}}
spec:
  selector:
    matchLabels:
      app: {{.ServerApp}}
  mtls:
    mode: STRICT
`)
				w.Expect(t, Request{}, check.OK())
			},
		},
		Feature{
			ID:          "mesh.authz.l4-principal",
			Description: "A DENY AuthorizationPolicy on the client's SPIFFE principal rejects its traffic: L4 identity is available to the workload proxy.",
			Roles:       []Role{RoleNode, RoleSidecar, RoleWaypoint},
			Covers: []string{"AuthorizationPolicy.selector", "AuthorizationPolicy.selector.matchLabels", "AuthorizationPolicy.action=DENY",
				"AuthorizationPolicy.rules.from.source.principals"},
			Run: func(t framework.TestContext, s *Scenario) {
				// The Target checks the path works first, so a broken client path fails there rather
				// than looking like a denial below. The policy attaches to the proxy that enforces it:
				// ztunnel and the server sidecar by selector, the waypoint by targetRefs.
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.AuthorizationPolicy("DENY", `
  - from:
    - source:
        principals: ["{{.ClientPrincipal}}"]
`))
				tg.Expect(t, Request{}, Denied())
			},
		},
		Feature{
			ID:          "mesh.authz.source-identity",
			Description: "AuthorizationPolicy source namespaces, serviceAccounts, notPrincipals and notNamespaces match the meshed client's identity.",
			Roles:       []Role{RoleSidecar, RoleWaypoint},
			RequiresL7:  true,
			Covers: []string{"AuthorizationPolicy.rules.from.source.namespaces", "AuthorizationPolicy.rules.from.source.serviceAccounts",
				"AuthorizationPolicy.rules.from.source.notPrincipals", "AuthorizationPolicy.rules.from.source.notNamespaces"},
			Run: func(t framework.TestContext, s *Scenario) {
				// One DENY policy, one path per rule. The positive matches (namespaces,
				// serviceAccounts) witness that the policy is in effect before the negated ones are
				// checked not to match the client.
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.AuthorizationPolicy("DENY", `
  - from: [{source: {namespaces: ["{{.ClientNamespace}}"]}}]
    to: [{operation: {paths: ["/ns"]}}]
  - from: [{source: {serviceAccounts: ["{{.ClientNamespace}}/{{.ClientSA}}"]}}]
    to: [{operation: {paths: ["/sa"]}}]
  - from: [{source: {notPrincipals: ["{{.ClientPrincipal}}"]}}]
    to: [{operation: {paths: ["/not-principal"]}}]
  - from: [{source: {notNamespaces: ["{{.ClientNamespace}}"]}}]
    to: [{operation: {paths: ["/not-namespace"]}}]
`))
				s.Case(t, "namespaces", func(t framework.TestContext) {
					tg.Expect(t, Request{Path: "/ns"}, check.Status(403))
				})
				s.Case(t, "serviceAccounts", func(t framework.TestContext) {
					tg.Expect(t, Request{Path: "/sa"}, check.Status(403))
				})
				tg.Expect(t, Request{Path: "/ns"}, check.Status(403))
				tg.ExpectStable(t, Request{Path: "/not-principal"}, check.OK())
				tg.ExpectStable(t, Request{Path: "/not-namespace"}, check.OK())
			},
		},
		Feature{
			ID:          "mesh.authz.ip-blocks",
			Description: "AuthorizationPolicy ipBlocks and remoteIpBlocks (and their negations) match the meshed client's address.",
			Roles:       []Role{RoleSidecar, RoleWaypoint},
			RequiresL7:  true,
			Covers: []string{"AuthorizationPolicy.rules.from.source.ipBlocks", "AuthorizationPolicy.rules.from.source.remoteIpBlocks",
				"AuthorizationPolicy.rules.from.source.notIpBlocks", "AuthorizationPolicy.rules.from.source.notRemoteIpBlocks"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				ip := tg.Workload().Client().WorkloadsOrFail(t)[0].Address()
				s.Apply(t, tg.AuthorizationPolicy("DENY", `
  - from: [{source: {ipBlocks: ["{{.ClientIP}}/32"]}}]
    to: [{operation: {paths: ["/ip"]}}]
  - from: [{source: {remoteIpBlocks: ["{{.ClientIP}}/32"]}}]
    to: [{operation: {paths: ["/remote-ip"]}}]
  - from: [{source: {notIpBlocks: ["{{.ClientIP}}/32"]}}]
    to: [{operation: {paths: ["/not-ip"]}}]
  - from: [{source: {notRemoteIpBlocks: ["{{.ClientIP}}/32"]}}]
    to: [{operation: {paths: ["/not-remote-ip"]}}]
`), map[string]any{"ClientIP": ip})
				s.Case(t, "ipBlocks", func(t framework.TestContext) {
					tg.Expect(t, Request{Path: "/ip"}, check.Status(403))
					tg.ExpectStable(t, Request{Path: "/not-ip"}, check.OK())
				})
				s.Case(t, "remoteIpBlocks", func(t framework.TestContext) {
					tg.Expect(t, Request{Path: "/remote-ip"}, check.Status(403))
					tg.ExpectStable(t, Request{Path: "/not-remote-ip"}, check.OK())
				})
			},
		},
		Feature{
			ID:          "mesh.authz.l7-path",
			Description: "A DENY AuthorizationPolicy on a request path rejects only that path.",
			Roles:       []Role{RoleSidecar, RoleWaypoint},
			RequiresL7:  true,
			Covers:      []string{"AuthorizationPolicy.rules.to.operation.paths"},
			Run: func(t framework.TestContext, s *Scenario) {
				// On the waypoint the policy must attach by targetRefs: a selector policy would be
				// enforced by ztunnel, which cannot evaluate paths.
				tg := s.Target(t, TargetOptions{})
				s.Apply(t, tg.AuthorizationPolicy("DENY", `
  - to:
    - operation:
        paths: ["/blocked"]
`))
				tg.Expect(t, Request{Path: "/ok"}, check.OK())
				tg.Expect(t, Request{Path: "/blocked"}, check.Status(403))
			},
		},
		Feature{
			ID:          "mesh.routing.path-match",
			Description: "A VirtualService routes by request path.",
			Roles:       []Role{RoleSidecar, RoleWaypoint},
			RequiresL7:  true,
			Covers: []string{"VirtualService.hosts", "VirtualService.http", "VirtualService.http.match",
				"VirtualService.http.match.uri", "VirtualService.http.match.uri.exact",
				"VirtualService.http.route", "VirtualService.http.route.destination", "VirtualService.http.route.destination.host"},
			Run: func(t framework.TestContext, s *Scenario) {
				w := s.Workload(t)
				s.Apply(t, meshVS(`
  - match:
    - uri: {exact: /admin}
    route: [{destination: {host: {{.ServerV2}}}}]
  - route: [{destination: {host: {{.ServerV1}}}}]
`))
				w.Expect(t, Request{Path: "/admin"}, Version("v2"))
				w.Expect(t, Request{Path: "/other"}, Version("v1"))
			},
		},
		Feature{
			ID:          "mesh.routing.header-match",
			Description: "A VirtualService routes by request header.",
			Roles:       []Role{RoleSidecar, RoleWaypoint},
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.match.headers", "VirtualService.http.match.headers.exact"},
			Run: func(t framework.TestContext, s *Scenario) {
				w := s.Workload(t)
				s.Apply(t, meshVS(`
  - match:
    - headers:
        x-user: {exact: alice}
    route: [{destination: {host: {{.ServerV2}}}}]
  - route: [{destination: {host: {{.ServerV1}}}}]
`))
				w.Expect(t, Request{Headers: H("x-user", "alice")}, Version("v2"))
				w.Expect(t, Request{Headers: H("x-user", "bob")}, Version("v1"))
			},
		},
		Feature{
			ID:          "mesh.routing.traffic-split",
			Description: "A VirtualService splits traffic by weight between backends.",
			Roles:       []Role{RoleSidecar, RoleWaypoint},
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.route.weight"},
			Run: func(t framework.TestContext, s *Scenario) {
				w := s.Workload(t)
				s.Apply(t, meshVS(`
  - route:
    - destination: {host: {{.ServerV1}}}
      weight: 60
    - destination: {host: {{.ServerV2}}}
      weight: 40
`))
				// v2 is unreachable without the route (the host under test serves v1), so this cannot
				// pass by chance; the retry absorbs sampling noise.
				w.Expect(t, Request{Count: SampleSize(60, 40)}, Distribution(map[string]int{"v1": 60, "v2": 40}))
			},
		},
		Feature{
			ID:          "mesh.routing.rewrite",
			Description: "A VirtualService rewrites the request path before forwarding.",
			Roles:       []Role{RoleSidecar, RoleWaypoint},
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.rewrite", "VirtualService.http.rewrite.uri", "VirtualService.http.match.uri.prefix"},
			Run: func(t framework.TestContext, s *Scenario) {
				w := s.Workload(t)
				s.Apply(t, meshVS(`
  - match:
    - uri: {prefix: /prefix}
    rewrite: {uri: /rewritten}
    route: [{destination: {host: {{.ServerV1}}}}]
`))
				// rewrite.uri replaces the matched prefix, so the remainder of the path is kept.
				w.Expect(t, Request{Path: "/prefix/x"}, Path("/rewritten/x"))
			},
		},
		Feature{
			ID:          "mesh.fault.abort",
			Description: "A VirtualService fault abort rejects requests with the configured status.",
			Roles:       []Role{RoleSidecar, RoleWaypoint},
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.fault", "VirtualService.http.fault.abort", "VirtualService.http.fault.abort.httpStatus", "VirtualService.http.fault.abort.percentage"},
			Run: func(t framework.TestContext, s *Scenario) {
				w := s.Workload(t)
				s.Apply(t, meshVS(`
  - fault:
      abort: {httpStatus: 418, percentage: {value: 100}}
    route: [{destination: {host: {{.ServerV1}}}}]
`))
				w.Expect(t, Request{}, check.Status(418))
			},
		},
		Feature{
			ID:          "mesh.timeout",
			Description: "A VirtualService timeout bounds the request; slow backends produce 504.",
			Roles:       []Role{RoleSidecar, RoleWaypoint},
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.timeout"},
			Run: func(t framework.TestContext, s *Scenario) {
				w := s.Workload(t)
				s.Apply(t, meshVS(`
  - timeout: 1s
    route: [{destination: {host: {{.ServerV1}}}}]
`))
				w.Expect(t, Request{Path: "/?delay=3s"}, check.Status(504))
				w.Expect(t, Request{Path: "/?delay=10ms"}, check.OK())
			},
		},
	)
}

// meshVS returns a mesh VirtualService for the scenario's server.
func meshVS(http string) string {
	return `
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.ServerSvc}}"]
  http:
` + http
}
