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
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
	"istio.io/istio/tests/common/jwt"
)

// Target is the proxy under test as a feature sees it, independent of the role. One feature body
// written against a Target runs on every role whose semantics are the same: an ingress gateway, a
// sidecar, a waypoint, or (for L4 behavior) the node proxy.
//
// A Target hides three things that differ between roles:
//
//   - Traffic: where requests go. Ingress: the out-of-mesh client calls a gateway of the proxy under
//     test. Workload roles: the meshed client calls the feature's own backend Service, and the proxy
//     under test intercepts it (client and server sidecars, or ztunnel and the waypoint).
//   - Routing: how config is bound to "this feature's host" (Host, template var .TargetHost).
//     Ingress: an HTTPRoute on the gateway, or a VirtualService bound to an Istio Gateway that
//     selects the gateway pods. Workload roles: a VirtualService for the backend Service with the
//     implicit "mesh" gateway, or an HTTPRoute whose parentRef is the backend Service (GAMMA).
//   - Policy attachment (template var .PolicyAttachment), following Istio's attachment rules:
//     ingress: targetRefs to the (dedicated) Gateway; sidecar and node: a selector on the server
//     workload, whose inbound proxy enforces server-side policy; waypoint: targetRefs to the backend
//     Service, which binds the policy to the waypoint serving it (selector policies never apply to
//     waypoints, they would be enforced by ztunnel at L4 instead).
//
// Destinations (template vars, set when the Target is created):
//
//	.TargetHost        the host requests carry and routing config matches (see Host)
//	.TargetGateway     the value for VirtualService gateways/match.gateways ("mesh" on workload roles)
//	.DestV1, .DestV2   hosts serving only v1 / only v2 of the backend
//	.Dest              a host serving both versions (pods labeled version: v1|v2, for DestinationRule subsets)
//	.DestApp           the app label of the backend pods (both versions)
//	.PolicyAttachment  targetRefs/selector lines for the spec of AuthorizationPolicy, RequestAuthentication, Telemetry
//	.WorkloadSelector  matchLabels (one "key: value" line) selecting the pods of the proxy under test that
//	                   enforce server-side policy: the gateway pods, or the backend pods (sidecar, node);
//	                   empty on waypoints, which selector policies never bind to
//
// .TargetHost serves v1 only, so v2 is reachable only through config under test (negative controls
// depend on it). Example:
//
//	tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
//	s.Apply(t, tg.VirtualService(`
//	  - match: [{headers: {x-v: {exact: "2"}}}]
//	    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
//	  - route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
//	`))
//	tg.Expect(t, Request{Headers: H("x-v", "2")}, Version("v2"))
//
//	tg := s.Target(t, TargetOptions{})
//	s.Apply(t, tg.AuthorizationPolicy("DENY", `
//	  - to: [{operation: {paths: ["/deny*"]}}]
//	`))
//	tg.Expect(t, Request{Path: "/deny/x"}, check.Status(403))
type Target struct {
	s    *Scenario
	gw   *Gateway
	wl   *Workload
	host string
}

// Routing selects how a Target's host is routed when the Target is created.
type Routing int

const (
	// RouteDefault routes the target host to .DestV1 before the feature applies anything, and checks
	// that it works, so later failures are attributable to the feature's config. Ingress: an
	// HTTPRoute on the gateway. Workload roles: the backend Service itself. Use it for policies.
	RouteDefault Routing = iota
	// RouteVirtualService leaves routing to a VirtualService the feature applies (VirtualService).
	// Ingress: an Istio Gateway server for the target host binds it to the gateway pods.
	RouteVirtualService
	// RouteNone applies no route: the feature routes the target host itself (HTTPRoute,
	// DestinationPolicy).
	RouteNone
)

// TargetOptions customizes Scenario.Target.
type TargetOptions struct {
	// Routing is how the target host is routed; see Routing.
	Routing Routing
	// Shared uses the gateway shared by all features of the proxy on gateway roles, instead of a
	// dedicated one. Only for route-level config: policies attached to a gateway apply to all of its
	// routes. Ignored on workload roles, where every feature has its own backend.
	Shared bool
}

// Target returns the proxy under test for this feature, creating what the role needs: a gateway
// (ingress), or the feature's backend (workload roles), plus the routing selected by o. Call it
// before applying config: it sets the template variables documented on Target.
func (s *Scenario) Target(t framework.TestContext, o TargetOptions) *Target {
	t.Helper()
	tg := &Target{s: s}
	if s.Proxy.isGateway() {
		s.targetGateway(t, tg, o)
	} else {
		s.targetWorkload(t, tg, o)
	}
	return tg
}

func (s *Scenario) targetGateway(t framework.TestContext, tg *Target, o TargetOptions) {
	if o.Shared {
		tg.gw = s.SharedGateway()
	} else {
		tg.gw = s.NewGateway(t, GatewayOptions{})
	}
	tg.host = s.Host()
	if o.Routing == RouteVirtualService {
		tg.host = "vs." + s.Host()
	}
	s.setTargetVars(map[string]any{
		"TargetHost":    tg.host,
		"TargetGateway": s.Name() + "-istio",
		"DestV1":        BackendSvc + "-v1",
		"DestV2":        BackendSvc + "-v2",
		"Dest":          BackendSvc,
		"DestApp":       BackendSvc,
		"PolicyAttachment": `  targetRefs:
  - {kind: Gateway, group: gateway.networking.k8s.io, name: ` + tg.gw.Name + `}`,
		"WorkloadSelector": "gateway.networking.k8s.io/gateway-name: " + tg.gw.Name,
	})
	switch o.Routing {
	case RouteDefault:
		s.Apply(t, tg.HTTPRoute(`
  - backendRefs:
    - {name: {{.DestV1}}, port: 80}
`), map[string]any{"Name": s.Name("route")})
		tg.Expect(t, Request{}, check.OK())
	case RouteVirtualService:
		s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: {{.TargetGateway}}
spec:
  selector:
    gateway.networking.k8s.io/gateway-name: {{.GW}}
  servers:
  - port: {number: 80, name: http, protocol: HTTP}
    hosts: ["{{.TargetHost}}"]
`, map[string]any{"GW": tg.gw.Name})
	}
}

func (s *Scenario) targetWorkload(t framework.TestContext, tg *Target, o TargetOptions) {
	tg.wl = s.Workload(t)
	be := s.backend
	tg.host = be.Service
	attach := `  selector:
    matchLabels:
      app: ` + be.App
	selector := "app: " + be.App
	if s.Proxy.Role == RoleWaypoint {
		selector = ""
		// Policies for a waypoint attach to what it serves; the per-feature Service keeps them off
		// the other features sharing the waypoint.
		attach = `  targetRefs:
  - {kind: Service, group: "", name: ` + be.Service + `}`
	}
	s.setTargetVars(map[string]any{
		"TargetHost":       be.Service,
		"TargetGateway":    "mesh",
		"DestV1":           be.V1,
		"DestV2":           be.V2,
		"Dest":             be.App,
		"DestApp":          be.App,
		"PolicyAttachment": attach,
		"WorkloadSelector": selector,
	})
	if s.Proxy.Role == RoleWaypoint {
		// The waypoint applies new HTTP filters (policies) only to new connections; see
		// NoConnectionReuse.
		s.NoConnectionReuse(t, be.Service)
	}
	if o.Routing == RouteDefault {
		tg.Expect(t, Request{}, check.OK())
	}
}

// setTargetVars adds template variables to everything the feature applies from now on.
func (s *Scenario) setTargetVars(v map[string]any) {
	if s.targetVars == nil {
		s.targetVars = map[string]any{}
	}
	for k, val := range v {
		s.targetVars[k] = val
	}
}

// Host is the host requests carry by default and that routing config matches (.TargetHost):
// "vs.<Host>" or Scenario.Host() on gateways, the backend Service name on workload roles.
func (tg *Target) Host() string { return tg.host }

// Gateway returns the gateway of a gateway-role Target, nil otherwise. Use it only for behavior that
// is genuinely gateway-specific (pod addresses, logs, metrics), and restrict the feature's Roles.
func (tg *Target) Gateway() *Gateway { return tg.gw }

// Workload returns the workload target of a workload-role Target, nil otherwise.
func (tg *Target) Workload() *Workload { return tg.wl }

// BackendInstances returns the echo instances behind .Dest (both versions), e.g. to read their logs.
func (tg *Target) BackendInstances() echo.Instances {
	if tg.gw != nil {
		return tg.s.Env.Backend
	}
	return tg.s.backend.Instances
}

func (tg *Target) request(r Request) Request {
	if r.Host == "" {
		r.Host = tg.host
	}
	return r
}

// Expect sends the request through the proxy under test, retrying until the check passes (config
// propagation is asynchronous) or the call timeout expires.
func (tg *Target) Expect(t framework.TestContext, r Request, check echo.Checker) echo.CallResult {
	t.Helper()
	if tg.gw != nil {
		return tg.gw.Expect(t, tg.request(r), check)
	}
	return tg.wl.Expect(t, tg.request(r), check)
}

// ExpectStable is like Expect but requires the check to pass consistently for a while; see
// Gateway.ExpectStable for when that is meaningful.
func (tg *Target) ExpectStable(t framework.TestContext, r Request, check echo.Checker) echo.CallResult {
	t.Helper()
	if tg.gw != nil {
		return tg.gw.ExpectStable(t, tg.request(r), check)
	}
	return tg.wl.ExpectStable(t, tg.request(r), check)
}

// Call sends a single request without retrying or checking, e.g. for timing.
func (tg *Target) Call(t framework.TestContext, r Request) (echo.CallResult, error) {
	t.Helper()
	if tg.gw != nil {
		return tg.gw.Call(t, tg.request(r))
	}
	return tg.wl.Call(t, tg.request(r))
}

// VirtualService returns a VirtualService template named .Name for the target host, bound like a
// route of this role (Istio Gateway on ingress, "mesh" on workload roles). http is the YAML list of
// spec.http items. Create the Target with RouteVirtualService.
func (tg *Target) VirtualService(http string) string {
	return `
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.TargetHost}}"]
  gateways: ["{{.TargetGateway}}"]
  http:
` + http
}

// HTTPRoute returns an HTTPRoute template named .Name for the target host. rules is the YAML list of
// spec.rules items. Ingress: parentRef the gateway, hostname the target host. Workload roles:
// parentRef the backend Service (GAMMA), which sidecars and waypoints serve.
func (tg *Target) HTTPRoute(rules string) string {
	parent := `  parentRefs:
  - {group: "", kind: Service, name: "{{.TargetHost}}", port: 80}
`
	if tg.gw != nil {
		parent = `  parentRefs:
  - name: ` + tg.gw.Name + `
  hostnames: ["{{.TargetHost}}"]
`
	}
	return `
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}
spec:
` + parent + `  rules:
` + rules
}

// AuthorizationPolicy returns an AuthorizationPolicy template attached to the proxy under test (see
// .PolicyAttachment) with the action and rules (YAML list items for spec.rules). The name is .Name,
// or .Name-<suffix>.
func (tg *Target) AuthorizationPolicy(action, rules string, suffix ...string) string {
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
{{.PolicyAttachment}}
  action: ` + action + `
  rules:
` + rules
}

// RequestAuthentication returns a RequestAuthentication template (.Name-jwt) for test issuer 1 with
// an inline JWKS, attached to the proxy under test. extra is appended to the jwtRule (indented 4).
func (tg *Target) RequestAuthentication(extra string) string {
	return `
apiVersion: security.istio.io/v1
kind: RequestAuthentication
metadata:
  name: {{.Name}}-jwt
spec:
{{.PolicyAttachment}}
  jwtRules:
  - issuer: test-issuer-1@istio.io
    jwks: '` + jwt.JwksJSON + `'
` + extra
}

// DestinationPolicy applies a DestinationRule trafficPolicy (YAML indented for spec.trafficPolicy)
// to a destination serving both backend versions, and routes the target host to it. Create the
// Target with RouteNone. The route is scaffolding (an HTTPRoute), so the negative control removes
// only the DestinationRule and the host still reaches both versions. Ingress: a per-feature
// ServiceEntry alias of the shared backend, so the policy affects no other feature. Workload roles:
// .Dest, the feature's own backend.
func (tg *Target) DestinationPolicy(t framework.TestContext, trafficPolicy string) {
	t.Helper()
	s := tg.s
	if tg.gw != nil {
		host := s.Name() + ".alias.pcs.example.com"
		s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.PolicyHost}}"]
  location: MESH_INTERNAL
  resolution: STATIC
  workloadSelector:
    labels:
      app: {{.DestApp}}
  ports:
  - {number: 80, name: http, protocol: HTTP, targetPort: 18080}
`, map[string]any{"PolicyHost": host})
		s.Apply(t, tg.HTTPRoute(`
  - backendRefs:
    - {group: networking.istio.io, kind: Hostname, name: {{.PolicyHost}}, port: 80}
`), map[string]any{"PolicyHost": host})
		s.Apply(t, destinationRule(trafficPolicy), map[string]any{"PolicyHost": host})
		return
	}
	s.Apply(t, tg.HTTPRoute(`
  - backendRefs:
    - {name: {{.Dest}}, port: 80}
`))
	s.Apply(t, destinationRule(trafficPolicy), map[string]any{"PolicyHost": s.targetVars["Dest"]})
}

func destinationRule(trafficPolicy string) string {
	return `
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}
spec:
  host: {{.PolicyHost}}
  trafficPolicy:
` + trafficPolicy
}
