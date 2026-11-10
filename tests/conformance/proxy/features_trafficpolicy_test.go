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
	"time"

	echoClient "istio.io/istio/pkg/test/echo"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
)

// DestinationRule traffic policy details (load balancer, outlier detection, connection pool, TLS),
// how DestinationRules for one host combine and are looked up, and the mesh-wide defaults.
func init() {
	Register(
		// --- loadBalancer ---
		Feature{
			ID:          "backend.destinationrule.consistent-hash-cookie-attributes",
			Description: "DestinationRule consistentHash.httpCookie attributes are added to the affinity cookie the proxy issues.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"DestinationRule.trafficPolicy.loadBalancer.consistentHash.httpCookie.attributes",
				"DestinationRule.trafficPolicy.loadBalancer.consistentHash.httpCookie.attributes.name",
				"DestinationRule.trafficPolicy.loadBalancer.consistentHash.httpCookie.attributes.value",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone, Shared: true})
				tg.DestinationPolicy(t, `
    loadBalancer:
      consistentHash:
        httpCookie:
          name: pcs-affinity
          ttl: 60s
          attributes:
          - {name: SameSite, value: Strict}
`)
				tg.Expect(t, Request{}, check.And(ResponseHeaderContains("Set-Cookie", "pcs-affinity="), ResponseHeaderContains("Set-Cookie", "SameSite=Strict")))
			},
		},

		// --- outlierDetection ---
		Feature{
			ID: "backend.destinationrule.outlier-gateway-errors",
			Description: "DestinationRule outlierDetection consecutiveGatewayErrors ejects endpoints answering 504 even with 5xx ejection " +
				"disabled; with every endpoint ejected the proxy answers 503.",
			Roles:      l7Roles,
			RequiresL7: true,
			Covers: []string{
				"DestinationRule.trafficPolicy.outlierDetection.consecutiveGatewayErrors", "DestinationRule.trafficPolicy.outlierDetection.consecutive5xxErrors",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone, Shared: true})
				// 504 is not retried by the default retry policy, so without ejection every response is the
				// backend's 504. Ejected endpoints are not used again for 10 minutes: no healthy upstream (503).
				tg.DestinationPolicy(t, `
    outlierDetection:
      consecutive5xxErrors: 0
      consecutiveGatewayErrors: 1
      interval: 1s
      baseEjectionTime: 10m
      maxEjectionPercent: 100
`)
				tg.Expect(t, Request{Path: "/?codes=504", Count: 10, Opts: newConnPerRequest}, someStatus(503))
			},
		},
		Feature{
			ID: "backend.destinationrule.outlier-http-error-codes",
			Description: "DestinationRule outlierDetection outlierDetectionHttpErrorCodes counts the listed status codes (429) as errors, " +
				"so endpoints answering them are ejected.",
			Roles:      l7Roles,
			RequiresL7: true,
			Covers:     []string{"DestinationRule.trafficPolicy.outlierDetection.outlierDetectionHttpErrorCodes"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone, Shared: true})
				tg.DestinationPolicy(t, `
    outlierDetection:
      consecutive5xxErrors: 1
      outlierDetectionHttpErrorCodes: [429]
      interval: 1s
      baseEjectionTime: 10m
      maxEjectionPercent: 100
`)
				tg.Expect(t, Request{Path: "/?codes=429", Count: 10, Opts: newConnPerRequest}, someStatus(503))
			},
		},
		Feature{
			ID: "backend.destinationrule.outlier-min-health-percent",
			Description: "DestinationRule outlierDetection minHealthPercent: while the healthy share of endpoints is at least the threshold " +
				"ejected endpoints are avoided; below it the proxy balances across all endpoints again (panic mode).",
			Roles:      l7Roles,
			RequiresL7: true,
			Covers:     []string{"DestinationRule.trafficPolicy.outlierDetection.minHealthPercent"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone, Shared: true})
				// One healthy and one broken endpoint (see backend.destinationrule.outlier-detection): with the
				// broken one ejected, 50% of the endpoints are healthy.
				host := tg.ServiceEntry(t, goodBadEndpoints(), goodBadVars(t, s))
				policy := func(pct int) string {
					return destinationRule(`
    outlierDetection:
      consecutive5xxErrors: 1
      interval: 1s
      baseEjectionTime: 10m
      maxEjectionPercent: 100
      minHealthPercent: ` + itoa(pct) + `
`)
				}
				s.Apply(t, policy(40), map[string]any{"PolicyHost": host})
				tg.Expect(t, Request{Count: 10, Opts: newConnPerRequest}, check.And(check.OK(), FromService(ExternalSvc)))
				s.Apply(t, policy(80), map[string]any{"PolicyHost": host})
				tg.ExpectStable(t, Request{Count: 20, Opts: newConnPerRequest}, some5xx())
			},
		},
		Feature{
			ID: "backend.destinationrule.outlier-local-origin",
			Description: "DestinationRule outlierDetection splitExternalLocalOriginErrors with consecutiveLocalOriginFailures ejects an " +
				"endpoint whose responses the proxy cannot parse (a locally detected failure).",
			Roles:      l7Roles,
			RequiresL7: true,
			Covers: []string{
				"DestinationRule.trafficPolicy.outlierDetection.splitExternalLocalOriginErrors",
				"DestinationRule.trafficPolicy.outlierDetection.consecutiveLocalOriginFailures",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone, Shared: true})
				host := tg.ServiceEntry(t, goodBadEndpoints(), goodBadVars(t, s))
				s.Apply(t, destinationRule(`
    outlierDetection:
      splitExternalLocalOriginErrors: true
      consecutiveLocalOriginFailures: 1
      consecutive5xxErrors: 0
      interval: 1s
      baseEjectionTime: 10m
      maxEjectionPercent: 100
`), map[string]any{"PolicyHost": host})
				tg.Expect(t, Request{Count: 10, Opts: newConnPerRequest}, check.And(check.OK(), FromService(ExternalSvc)))
			},
		},

		// --- connectionPool ---
		Feature{
			ID:          "backend.destinationrule.connect-timeout",
			Description: "DestinationRule connectionPool.tcp.connectTimeout bounds connection attempts to an unreachable endpoint (503 quickly).",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers:      []string{"DestinationRule.trafficPolicy.connectionPool.tcp.connectTimeout", "DestinationRule.trafficPolicy.connectionPool.tcp"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone, Shared: true})
				host := tg.ServiceEntry(t, blackholeEndpoint)
				s.Apply(t, destinationRule(`
    connectionPool:
      tcp:
        connectTimeout: 500ms
`), map[string]any{"PolicyHost": host})
				// The default connect timeout (10s) outlasts the client's: without the policy there is no response.
				tg.Expect(t, Request{Opts: clientTimeout(5 * time.Second)}, check.Status(503))
			},
		},
		Feature{
			ID: "backend.destinationrule.h2-upgrade-policy-override",
			Description: "DestinationRule connectionPool.http.h2UpgradePolicy overrides MeshConfig h2UpgradePolicy UPGRADE per destination: " +
				"DEFAULT follows the mesh setting (HTTP/2), DO_NOT_UPGRADE keeps HTTP/1.1.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers: []string{
				"DestinationRule.trafficPolicy.connectionPool.http.h2UpgradePolicy=DEFAULT",
				"DestinationRule.trafficPolicy.connectionPool.http.h2UpgradePolicy=DO_NOT_UPGRADE",
				"MeshConfig.h2UpgradePolicy",
			},
			MeshConfig: `
h2UpgradePolicy: UPGRADE
`,
			Run: func(t framework.TestContext, s *Scenario) {
				// Out-of-mesh backend: an in-mesh sidecar would re-encode the request toward the app.
				gw := s.SharedGateway()
				s.Apply(t, externalAlias(`
    connectionPool:
      http:
        h2UpgradePolicy: DEFAULT
`), s.sharedVars(), map[string]any{"Name": s.Name("default"), "Host": s.Host("default")})
				gw.Expect(t, Request{Host: s.Host("default")}, check.And(check.OK(), check.Protocol("HTTP/2.0")))
				s.Apply(t, externalAlias(`
    connectionPool:
      http:
        h2UpgradePolicy: DO_NOT_UPGRADE
`), s.sharedVars(), map[string]any{"Name": s.Name("keep"), "Host": s.Host("keep")})
				gw.ExpectStable(t, Request{Host: s.Host("keep")}, check.And(check.OK(), check.Protocol("HTTP/1.1")))
			},
		},

		// --- tls ---
		Feature{
			ID: "backend.destinationrule.tls-ca-cert-credential-name",
			Description: "DestinationRule tls.caCertCredentialName verifies the backend certificate against the CA in the named Secret: " +
				"the right CA connects, another CA fails (503).",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"DestinationRule.trafficPolicy.tls.caCertCredentialName"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.SharedGateway()
				s.CAConfigSecret(t, s.Name("ca"), echoTestCA())
				s.CAConfigSecret(t, s.Name("other-ca"), newCA(t).pem)
				s.Apply(t, caCertOrigination(), s.sharedVars(), map[string]any{"CA": s.Name("ca"), "TLSHost": BackendTLSHost})
				gw.Expect(t, Request{}, check.And(FromService(ExternalSvc), check.SNI(BackendTLSHost)))
				s.Apply(t, caCertOrigination(), s.sharedVars(), map[string]any{
					"Name": s.Name("other"), "Host": s.Host("other"), "CA": s.Name("other-ca"), "TLSHost": BackendTLSHost,
				})
				gw.ExpectStable(t, Request{Host: s.Host("other")}, check.Status(503))
			},
		},

		// --- How DestinationRules combine and are found ---
		Feature{
			ID: "backend.destinationrule.merge",
			Description: "Two DestinationRules for one host are merged: the subsets of both are usable, and the top-level trafficPolicy of " +
				"the older one wins.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"interaction:destinationrule.merge", "DestinationRule.subsets"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService, Shared: true})
				vars := map[string]any{"Alias": s.Name() + ".alias.pcs.example.com"}
				s.Apply(t, backendAliasSE, vars)
				s.Apply(t, tg.VirtualService(`
  - match: [{uri: {prefix: /v1}}]
    route: [{destination: {host: "{{.Alias}}", subset: v1, port: {number: 80}}}]
  - route: [{destination: {host: "{{.Alias}}", subset: v2, port: {number: 80}}}]
`), vars)
				s.Apply(t, subsetCookieRule("first", "v1"), vars)
				tg.Expect(t, Request{Path: "/v1"}, check.And(Version("v1"), ResponseHeaderContains("Set-Cookie", "pcs-first=")))
				// Subset v2 is defined only by the newer rule; the merged rule keeps the older top-level policy.
				s.Apply(t, subsetCookieRule("second", "v2"), vars)
				tg.ExpectStable(t, Request{}, check.And(
					Version("v2"), ResponseHeaderContains("Set-Cookie", "pcs-first="), responseHeaderNotContains("Set-Cookie", "pcs-second="),
				))
			},
		},
		Feature{
			ID: "backend.destinationrule.lookup-namespace",
			Description: "A gateway uses the DestinationRule for a host from its own namespace first, then the host's namespace, then " +
				"the root namespace.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"interaction:destinationrule.lookup-namespace"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.SharedGateway()
				ext, root := s.Env.ExternalNamespace.Name(), s.Env.SystemNamespace
				// Each host is a ServiceEntry in the external namespace, so the host's namespace differs from
				// the gateway's. The DestinationRules differ only in the affinity cookie name.
				cases := []struct {
					name string
					drs  map[string]string // namespace -> cookie
					want string
				}{
					{"local", map[string]string{s.Namespace(): "pcs-local", ext: "pcs-host"}, "pcs-local"},
					{"host", map[string]string{ext: "pcs-host", root: "pcs-root"}, "pcs-host"},
					{"root", map[string]string{root: "pcs-root"}, "pcs-root"},
				}
				for _, c := range cases {
					vars := map[string]any{"Name": s.Name(c.name), "Host": s.Host(c.name)}
					s.ApplyTo(t, ext, externalServiceEntry(), vars)
					s.Apply(t, externalHostRoute, s.sharedVars(), vars)
					for ns, cookie := range c.drs {
						s.ApplyTo(t, ns, cookieRule(cookie, ""), vars)
					}
				}
				for _, c := range cases {
					gw.ExpectStable(t, Request{Host: s.Host(c.name)}, check.And(check.OK(), ResponseHeaderContains("Set-Cookie", c.want+"=")))
				}
			},
		},
		Feature{
			ID: "backend.destinationrule.export-to",
			Description: "DestinationRule exportTo: a rule exported to all namespaces applies to a gateway in another namespace, a rule " +
				"exported only to its own namespace does not.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"DestinationRule.exportTo", "interaction:visibility.export-to"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.SharedGateway()
				ext := s.Env.ExternalNamespace.Name()
				for name, exportTo := range map[string]string{"all": `["*"]`, "local": `["."]`} {
					vars := map[string]any{"Name": s.Name(name), "Host": s.Host(name)}
					s.ApplyTo(t, ext, externalServiceEntry(), vars)
					s.Apply(t, externalHostRoute, s.sharedVars(), vars)
					s.ApplyTo(t, ext, cookieRule("pcs-exported", exportTo), vars)
				}
				gw.Expect(t, Request{Host: s.Host("all")}, check.And(check.OK(), ResponseHeaderContains("Set-Cookie", "pcs-exported=")))
				gw.ExpectStable(t, Request{Host: s.Host("local")}, check.And(check.OK(), ResponseHeaderAbsent("Set-Cookie")))
			},
		},
		Feature{
			ID: "backend.destinationrule.workload-selector-gateway",
			Description: "A DestinationRule with a workloadSelector applies to the selected gateway, which ignores the namespace's " +
				"catch-all rule for the host; other gateways keep using the catch-all rule.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"DestinationRule.workloadSelector", "DestinationRule.workloadSelector.matchLabels"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				shared := s.SharedGateway()
				vars := map[string]any{"Alias": s.Name() + ".alias.pcs.example.com", "GW": gw.Name}
				s.Apply(t, backendAliasSE, vars)
				s.Apply(t, `
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}
spec:
  parentRefs: [{name: {{.GW}}}, {name: {{.SharedGateway}}}]
  hostnames: ["{{.Host}}"]
  rules:
  - backendRefs:
    - {group: networking.istio.io, kind: Hostname, name: "{{.Alias}}", port: 80}
---
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}-all
spec:
  host: "{{.Alias}}"
  trafficPolicy:
    loadBalancer:
      consistentHash:
        httpCookie: {name: pcs-all, ttl: 60s}
---
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}-selected
spec:
  host: "{{.Alias}}"
  workloadSelector:
    matchLabels:
      gateway.networking.k8s.io/gateway-name: {{.GW}}
  trafficPolicy:
    loadBalancer:
      consistentHash:
        httpCookie: {name: pcs-selected, ttl: 60s}
`, s.sharedVars(), vars)
				gw.Expect(t, Request{}, check.And(check.OK(), ResponseHeaderContains("Set-Cookie", "pcs-selected=")))
				shared.ExpectStable(t, Request{}, check.And(check.OK(), ResponseHeaderContains("Set-Cookie", "pcs-all="),
					responseHeaderNotContains("Set-Cookie", "pcs-selected=")))
			},
		},

		// --- Locality load balancing. The gateway's locality is the topology.istio.io/locality label of its
		// pods (region1/zone1/sub1); ServiceEntry endpoints declare theirs, so one node can host several. ---
		Feature{
			ID: "backend.destinationrule.locality-enabled",
			Description: "DestinationRule localityLbSetting enabled (with outlierDetection) sends all traffic to the endpoint in the " +
				"gateway's locality; enabled: false balances across localities.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers: []string{
				"DestinationRule.trafficPolicy.loadBalancer.localityLbSetting", "DestinationRule.trafficPolicy.loadBalancer.localityLbSetting.enabled",
				"ServiceEntry.endpoints.locality", "label:topology.istio.io/locality",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := localityGateway(t, s)
				ip := echoPodIPs(t, s)
				endpoints := localityEndpoint(ip.v1, 18080, "region1/zone1/sub1", "") + localityEndpoint(ip.external, 18080, "region2/zone2/sub2", "")
				on := localityHost(t, s, gw, "on", endpoints, `
    loadBalancer:
      localityLbSetting: {enabled: true}
`+passiveOutlier)
				gw.Expect(t, Request{Host: on, Count: 20}, FromService(BackendSvc+"-v1"))
				off := localityHost(t, s, gw, "off", endpoints, `
    loadBalancer:
      localityLbSetting: {enabled: false}
`+passiveOutlier)
				gw.Expect(t, Request{Host: off, Count: 20}, check.And(check.OK(), someFromService(BackendSvc+"-v1"), someFromService(ExternalSvc)))
			},
		},
		Feature{
			ID:          "backend.destinationrule.locality-distribute",
			Description: "DestinationRule localityLbSetting distribute sends the traffic of the gateway's locality to the configured locality.",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers: []string{
				"DestinationRule.trafficPolicy.loadBalancer.localityLbSetting.distribute",
				"DestinationRule.trafficPolicy.loadBalancer.localityLbSetting.distribute.from",
				"DestinationRule.trafficPolicy.loadBalancer.localityLbSetting.distribute.to", "interaction:locality.distribution",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := localityGateway(t, s)
				ip := echoPodIPs(t, s)
				host := localityHost(t, s, gw, "", localityEndpoint(ip.v1, 18080, "region1/zone1/sub1", "")+
					localityEndpoint(ip.external, 18080, "region2/zone2/sub2", ""), `
    loadBalancer:
      localityLbSetting:
        distribute:
        - from: region1/zone1/*
          to: {"region2/zone2/*": 100}
`)
				gw.Expect(t, Request{Host: host, Count: 20}, FromService(ExternalSvc))
			},
		},
		Feature{
			ID: "backend.destinationrule.locality-failover",
			Description: "DestinationRule localityLbSetting failover: when the endpoints of the gateway's region are ejected, traffic " +
				"fails over to the configured region rather than any other.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers: []string{
				"DestinationRule.trafficPolicy.loadBalancer.localityLbSetting.failover",
				"DestinationRule.trafficPolicy.loadBalancer.localityLbSetting.failover.from",
				"DestinationRule.trafficPolicy.loadBalancer.localityLbSetting.failover.to", "interaction:locality.failover",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := localityGateway(t, s)
				host := localityHost(t, s, gw, "", failoverEndpoints(t, s), `
    loadBalancer:
      localityLbSetting:
        failover:
        - {from: region1, to: region3}
`+ejectingOutlier)
				gw.Expect(t, Request{Host: host, Count: 10, Opts: newConnPerRequest}, FromService(ExternalSvc))
			},
		},
		Feature{
			ID: "backend.destinationrule.locality-failover-priority",
			Description: "DestinationRule localityLbSetting failoverPriority prefers endpoints whose labels match the gateway's over " +
				"endpoints in the gateway's locality.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"DestinationRule.trafficPolicy.loadBalancer.localityLbSetting.failoverPriority"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := localityGateway(t, s)
				host := localityHost(t, s, gw, "", priorityEndpoints(t, s), `
    loadBalancer:
      localityLbSetting:
        failoverPriority: [pcs-tier]
`+passiveOutlier)
				gw.Expect(t, Request{Host: host, Count: 20}, FromService(ExternalSvc))
			},
		},
		Feature{
			ID: "backend.destinationrule.zone-aware",
			Description: "DestinationRule zoneAwareLbSetting keeps traffic in the gateway's region (other zones included) without outlier " +
				"detection, fails over to the configured region, and applies failoverPriority labels first.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers: []string{
				"DestinationRule.trafficPolicy.loadBalancer.zoneAwareLbSetting", "DestinationRule.trafficPolicy.loadBalancer.zoneAwareLbSetting.enabled",
				"DestinationRule.trafficPolicy.loadBalancer.zoneAwareLbSetting.failover",
				"DestinationRule.trafficPolicy.loadBalancer.zoneAwareLbSetting.failover.from",
				"DestinationRule.trafficPolicy.loadBalancer.zoneAwareLbSetting.failover.to",
				"DestinationRule.trafficPolicy.loadBalancer.zoneAwareLbSetting.failoverPriority",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := localityGateway(t, s)
				ip := echoPodIPs(t, s)
				region := localityHost(t, s, gw, "region", localityEndpoint(ip.v1, 18080, "region1/zone2/sub2", "")+
					localityEndpoint(ip.external, 18080, "region2/zone1/sub1", ""), `
    loadBalancer:
      zoneAwareLbSetting: {enabled: true}
`)
				gw.Expect(t, Request{Host: region, Count: 20}, FromService(BackendSvc+"-v1"))
				failover := localityHost(t, s, gw, "failover", failoverEndpoints(t, s), `
    loadBalancer:
      zoneAwareLbSetting:
        enabled: true
        failover:
        - {from: region1, to: region3}
`+ejectingOutlier)
				gw.Expect(t, Request{Host: failover, Count: 10, Opts: newConnPerRequest}, FromService(ExternalSvc))
				priority := localityHost(t, s, gw, "priority", priorityEndpoints(t, s), `
    loadBalancer:
      zoneAwareLbSetting:
        enabled: true
        failoverPriority: [pcs-tier]
`)
				gw.Expect(t, Request{Host: priority, Count: 20}, FromService(ExternalSvc))
			},
		},

		// --- MeshConfig defaults for destinations ---
		Feature{
			ID: "backend.meshconfig.default-traffic-policy",
			Description: "MeshConfig defaultTrafficPolicy connectionPool limits apply to destinations without a DestinationRule connectionPool, " +
				"and a DestinationRule connectionPool replaces them.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"MeshConfig.defaultTrafficPolicy", "interaction:destinationrule.mesh-default-traffic-policy"},
			MeshConfig: `
defaultTrafficPolicy:
  connectionPool:
    tcp: {maxConnections: 1}
    http: {http1MaxPendingRequests: 1}
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.SharedGateway()
				overflow := check.And(someStatus(200), someStatus(503))
				// No DestinationRule: the mesh limits apply.
				s.Apply(t, externalServiceEntry()+"---"+externalHostRoute, s.sharedVars(), map[string]any{"Name": s.Name("none"), "Host": s.Host("none")})
				gw.Expect(t, Request{Host: s.Host("none"), Path: "/?delay=1s", Count: 20}, overflow)
				// A DestinationRule without connectionPool keeps the mesh limits.
				s.Apply(t, externalAlias(`
    loadBalancer: {simple: ROUND_ROBIN}
`), s.sharedVars(), map[string]any{"Name": s.Name("lb"), "Host": s.Host("lb")})
				gw.Expect(t, Request{Host: s.Host("lb"), Path: "/?delay=1s", Count: 20}, overflow)
				// A DestinationRule connectionPool replaces them.
				s.Apply(t, externalAlias(`
    connectionPool:
      tcp: {maxConnections: 1000}
      http: {http1MaxPendingRequests: 1000}
`), s.sharedVars(), map[string]any{"Name": s.Name("own"), "Host": s.Host("own")})
				gw.ExpectStable(t, Request{Host: s.Host("own"), Path: "/?delay=1s", Count: 20}, check.OK())
			},
		},
		Feature{
			ID:          "backend.meshconfig.connect-timeout",
			Description: "MeshConfig connectTimeout bounds connection attempts to an unreachable endpoint (503 quickly).",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers:      []string{"MeshConfig.connectTimeout"},
			MeshConfig: `
connectTimeout: 500ms
`,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone, Shared: true})
				tg.ServiceEntry(t, blackholeEndpoint)
				tg.Expect(t, Request{Opts: clientTimeout(5 * time.Second)}, check.Status(503))
			},
		},
		Feature{
			ID: "backend.meshconfig.default-destination-rule-export-to",
			Description: "MeshConfig defaultDestinationRuleExportTo [\".\"] hides DestinationRules without exportTo from gateways in other " +
				"namespaces; an explicit exportTo still exports them.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"MeshConfig.defaultDestinationRuleExportTo", "DestinationRule.exportTo"},
			MeshConfig: `
defaultDestinationRuleExportTo: ["."]
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.SharedGateway()
				ext := s.Env.ExternalNamespace.Name()
				for name, exportTo := range map[string]string{"explicit": `["*"]`, "default": ""} {
					vars := map[string]any{"Name": s.Name(name), "Host": s.Host(name)}
					s.ApplyTo(t, ext, externalServiceEntry(), vars)
					s.Apply(t, externalHostRoute, s.sharedVars(), vars)
					s.ApplyTo(t, ext, cookieRule("pcs-exported", exportTo), vars)
				}
				gw.Expect(t, Request{Host: s.Host("explicit")}, check.And(check.OK(), ResponseHeaderContains("Set-Cookie", "pcs-exported=")))
				gw.ExpectStable(t, Request{Host: s.Host("default")}, check.And(check.OK(), ResponseHeaderAbsent("Set-Cookie")))
			},
		},
		Feature{
			ID:          "backend.meshconfig.locality-distribute",
			Description: "MeshConfig localityLbSetting distribute applies to destinations without a DestinationRule.",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers: []string{
				"MeshConfig.localityLbSetting", "MeshConfig.localityLbSetting.enabled", "MeshConfig.localityLbSetting.distribute",
				"MeshConfig.localityLbSetting.distribute.from", "MeshConfig.localityLbSetting.distribute.to",
			},
			MeshConfig: `
localityLbSetting:
  enabled: true
  distribute:
  - from: region1/zone1/*
    to: {"region2/zone2/*": 100}
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := localityGateway(t, s)
				ip := echoPodIPs(t, s)
				host := localityHost(t, s, gw, "", localityEndpoint(ip.v1, 18080, "region1/zone1/sub1", "")+
					localityEndpoint(ip.external, 18080, "region2/zone2/sub2", ""), "")
				gw.Expect(t, Request{Host: host, Count: 20}, FromService(ExternalSvc))
			},
		},
		Feature{
			ID:          "backend.meshconfig.locality-failover",
			Description: "MeshConfig localityLbSetting failover applies to destinations whose DestinationRule sets only outlierDetection.",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers: []string{
				"MeshConfig.localityLbSetting.failover", "MeshConfig.localityLbSetting.failover.from", "MeshConfig.localityLbSetting.failover.to",
			},
			MeshConfig: `
localityLbSetting:
  enabled: true
  failover:
  - {from: region1, to: region3}
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := localityGateway(t, s)
				host := localityHost(t, s, gw, "", failoverEndpoints(t, s), ejectingOutlier)
				gw.Expect(t, Request{Host: host, Count: 10, Opts: newConnPerRequest}, FromService(ExternalSvc))
			},
		},
		Feature{
			ID: "backend.meshconfig.locality-failover-priority",
			Description: "MeshConfig localityLbSetting failoverPriority applies to destinations whose DestinationRule sets only " +
				"outlierDetection, preferring endpoints whose labels match the gateway's.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"MeshConfig.localityLbSetting.failoverPriority"},
			MeshConfig: `
localityLbSetting:
  enabled: true
  failoverPriority: [pcs-tier]
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := localityGateway(t, s)
				host := localityHost(t, s, gw, "", priorityEndpoints(t, s), passiveOutlier)
				gw.Expect(t, Request{Host: host, Count: 20}, FromService(ExternalSvc))
			},
		},
		Feature{
			ID: "backend.workloadentry.ports-labels",
			Description: "A ServiceEntry workloadSelector selects WorkloadEntries by labels; traffic reaches each entry's address " +
				"on the target port its ports map gives the ServiceEntry port name.",
			Roles: ingressRole,
			Covers: []string{
				"WorkloadEntry.address", "WorkloadEntry.labels", "WorkloadEntry.ports",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				// The echo listens on 18080 only, so the default target port (the ServiceEntry port 80)
				// is refused; the unselected entry would answer from backend v2.
				gw := s.SharedGateway()
				ip := echoPodIPs(t, s)
				s.Apply(t, workloadEntry("v1", ip.v1, "")+workloadEntry("v2", ip.v2, "")+`
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.Name}}.{{.ExternalHost}}"]
  location: MESH_INTERNAL
  resolution: STATIC
  workloadSelector:
    labels: {pcs-we: {{.Name}}-v1}
  ports:
  - {number: 80, name: http, protocol: HTTP}
---`+externalHostRoute, s.sharedVars())
				gw.Expect(t, Request{Count: 10}, FromService(BackendSvc+"-v1"))
			},
		},
		Feature{
			ID:          "backend.workloadentry.locality",
			Description: "WorkloadEntry locality places the entry for locality load balancing: all traffic goes to the entry in the gateway's locality.",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers:      []string{"WorkloadEntry.locality"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := localityGateway(t, s)
				ip := echoPodIPs(t, s)
				s.Apply(t, workloadEntry("local", ip.v1, "region1/zone1/sub1")+
					workloadEntry("remote", ip.external, "region2/zone2/sub2")+`
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.Name}}.{{.ExternalHost}}"]
  location: MESH_INTERNAL
  resolution: STATIC
  workloadSelector:
    labels: {pcs-we-group: {{.Name}}}
  ports:
  - {number: 80, name: http, protocol: HTTP}
---`+destinationRule(`
    loadBalancer:
      localityLbSetting: {enabled: true}
`+passiveOutlier)+`
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
    - {group: networking.istio.io, kind: Hostname, name: "{{.Name}}.{{.ExternalHost}}", port: 80}
`, map[string]any{"GW": gw.Name, "PolicyHost": s.Name() + "." + ExternalHost})
				gw.Expect(t, Request{Count: 20}, FromService(BackendSvc+"-v1"))
			},
		},
		Feature{
			ID:          "backend.meshconfig.zone-aware",
			Description: "MeshConfig zoneAwareLbSetting applies to destinations without a DestinationRule.",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers: []string{
				"MeshConfig.zoneAwareLbSetting", "MeshConfig.zoneAwareLbSetting.enabled", "MeshConfig.zoneAwareLbSetting.failoverPriority",
			},
			MeshConfig: `
zoneAwareLbSetting:
  enabled: true
  failoverPriority: [pcs-tier]
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := localityGateway(t, s)
				ip := echoPodIPs(t, s)
				// The endpoints carry no pcs-tier label, so only the region decides.
				region := localityHost(t, s, gw, "region", localityEndpoint(ip.v1, 18080, "region1/zone2/sub2", "")+
					localityEndpoint(ip.external, 18080, "region2/zone1/sub1", ""), "")
				gw.Expect(t, Request{Host: region, Count: 20}, FromService(BackendSvc+"-v1"))
				priority := localityHost(t, s, gw, "priority", priorityEndpoints(t, s), "")
				gw.Expect(t, Request{Host: priority, Count: 20}, FromService(ExternalSvc))
			},
		},
	)
}

// workloadEntry is a WorkloadEntry {{.Name}}-<suffix> (a YAML document) labeled pcs-we={{.Name}}-<suffix>
// and pcs-we-group={{.Name}}, with the address, the echo's HTTP port and an optional locality.
func workloadEntry(suffix, address, locality string) string {
	loc := ""
	if locality != "" {
		loc = "\n  locality: " + locality
	}
	return `
apiVersion: networking.istio.io/v1
kind: WorkloadEntry
metadata:
  name: {{.Name}}-` + suffix + `
spec:
  address: ` + address + `
  labels: {pcs-we: "{{.Name}}-` + suffix + `", pcs-we-group: "{{.Name}}"}
  ports: {http: 18080}` + loc + `
---
`
}

// passiveOutlier enables outlier detection (required for locality failover priorities) without
// ejecting healthy endpoints. YAML indented for spec.trafficPolicy.
const passiveOutlier = `
    outlierDetection:
      consecutive5xxErrors: 100
      interval: 1s
      baseEjectionTime: 1s
`

// ejectingOutlier ejects an endpoint after its first 5xx, for 10 minutes.
const ejectingOutlier = `
    outlierDetection:
      consecutive5xxErrors: 1
      interval: 1s
      baseEjectionTime: 10m
      maxEjectionPercent: 100
`

// blackholeEndpoint is a ServiceEntry spec (see Target.ServiceEntry) whose only endpoint is a
// documentation address (TEST-NET-1) that never answers: connection attempts time out.
const blackholeEndpoint = `
  location: MESH_EXTERNAL
  resolution: STATIC
  ports:
  - {number: 80, name: http, protocol: HTTP}
  endpoints:
  - address: 192.0.2.1
`

func clientTimeout(d time.Duration) func(*echo.CallOptions) {
	return func(o *echo.CallOptions) { o.Timeout = d }
}

// goodBadEndpoints is a ServiceEntry spec (see Target.ServiceEntry) with one healthy endpoint (the
// external echo) and one that answers HTTP with garbage (a backend pod's TCP echo port, a 502 the
// default retry policy does not retry). Template variables: goodBadVars.
func goodBadEndpoints() string {
	return `
  location: MESH_EXTERNAL
  resolution: STATIC
  ports:
  - {number: 80, name: http, protocol: HTTP}
  endpoints:
  - {address: {{.GoodIP}}, ports: {http: 18080}}
  - {address: {{.BadIP}}, ports: {http: 19090}}
`
}

func goodBadVars(t framework.TestContext, s *Scenario) map[string]any {
	ip := echoPodIPs(t, s)
	return map[string]any{"GoodIP": ip.external, "BadIP": ip.v1}
}

type podIPs struct{ v1, v2, external string }

// echoPodIPs returns the pod IPs of the shared backend's versions and of the external echo.
func echoPodIPs(t framework.TestContext, s *Scenario) podIPs {
	t.Helper()
	var ip podIPs
	for _, w := range s.Env.Backend[0].WorkloadsOrFail(t) {
		switch {
		case strings.Contains(w.PodName(), "-v1-"):
			ip.v1 = w.Address()
		case strings.Contains(w.PodName(), "-v2-"):
			ip.v2 = w.Address()
		}
	}
	ip.external = s.Env.External[0].WorkloadsOrFail(t)[0].Address()
	if ip.v1 == "" || ip.v2 == "" {
		t.Fatalf("backend v1/v2 pods not found: %+v", ip)
	}
	return ip
}

// localityGateway deploys a gateway whose pods are in locality region1/zone1/sub1 and carry the
// label pcs-tier=local (for failoverPriority).
func localityGateway(t framework.TestContext, s *Scenario) *Gateway {
	t.Helper()
	return s.NewGateway(t, GatewayOptions{Infrastructure: `
    labels:
      topology.istio.io/locality: region1.zone1.sub1
      pcs-tier: local`})
}

// localityEndpoint is a ServiceEntry endpoint (YAML list item for spec.endpoints) in the locality,
// with an optional pcs-tier label.
func localityEndpoint(ip string, port int, locality, tier string) string {
	labels := ""
	if tier != "" {
		labels = ", labels: {pcs-tier: " + tier + "}"
	}
	return fmt.Sprintf("  - {address: %s, ports: {http: %d}, locality: %s%s}\n", ip, port, locality, labels)
}

// failoverEndpoints: a broken endpoint in the gateway's region (region1), and healthy ones in
// region2 (backend v2) and region3 (external echo). Once the broken one is ejected, default failover
// spreads traffic across region2 and region3.
func failoverEndpoints(t framework.TestContext, s *Scenario) string {
	ip := echoPodIPs(t, s)
	return localityEndpoint(ip.v1, 19090, "region1/zone1/sub1", "") + localityEndpoint(ip.v2, 18080, "region2/zone2/sub2", "") +
		localityEndpoint(ip.external, 18080, "region3/zone3/sub3", "")
}

// priorityEndpoints: backend v1 in the gateway's locality with pcs-tier=other, and the external
// echo in another region with the gateway's pcs-tier=local.
func priorityEndpoints(t framework.TestContext, s *Scenario) string {
	ip := echoPodIPs(t, s)
	return localityEndpoint(ip.v1, 18080, "region1/zone1/sub1", "other") + localityEndpoint(ip.external, 18080, "region2/zone2/sub2", "local")
}

// localityHost routes a per-feature host on gw to a ServiceEntry with the given endpoints (see
// localityEndpoint) and applies trafficPolicy (YAML indented for spec.trafficPolicy; empty: no
// DestinationRule) to it. It returns the host to request.
func localityHost(t framework.TestContext, s *Scenario, gw *Gateway, name, endpoints, trafficPolicy string) string {
	t.Helper()
	if name == "" {
		name = "lb"
	}
	vars := map[string]any{"Name": s.Name(name), "Host": s.Host(name), "GW": gw.Name, "Endpoints": endpoints}
	s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.Name}}.{{.ExternalHost}}"]
  location: MESH_EXTERNAL
  resolution: STATIC
  ports:
  - {number: 80, name: http, protocol: HTTP}
  endpoints:
{{.Endpoints}}
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
    - {group: networking.istio.io, kind: Hostname, name: "{{.Name}}.{{.ExternalHost}}", port: 80}
`, vars)
	if trafficPolicy != "" {
		s.Apply(t, destinationRule(trafficPolicy), vars, map[string]any{"PolicyHost": s.Name(name) + "." + ExternalHost})
	}
	return s.Host(name)
}

// backendAliasSE is a ServiceEntry for host .Alias selecting both backend versions.
const backendAliasSE = `
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}-alias
spec:
  hosts: ["{{.Alias}}"]
  location: MESH_INTERNAL
  resolution: STATIC
  workloadSelector:
    labels:
      app: {{.Backend}}
  ports:
  - {number: 80, name: http, protocol: HTTP, targetPort: 18080}
`

// externalHostRoute routes .Host on the shared gateway to the ServiceEntry host of
// externalServiceEntry (.Name.ExternalHost), wherever that ServiceEntry lives.
const externalHostRoute = `
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

// cookieRule is a DestinationRule for the externalServiceEntry host whose only observable effect is
// an affinity cookie with the given name, optionally with exportTo (a YAML list).
func cookieRule(cookie, exportTo string) string {
	export := ""
	if exportTo != "" {
		export = "\n  exportTo: " + exportTo
	}
	return `
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}-` + cookie + `
spec:
  host: "{{.Name}}.{{.ExternalHost}}"` + export + `
  trafficPolicy:
    loadBalancer:
      consistentHash:
        httpCookie: {name: ` + cookie + `, ttl: 60s}
`
}

// subsetCookieRule is a DestinationRule for .Alias defining one subset (a backend version) and an
// affinity cookie pcs-<name>.
func subsetCookieRule(name, version string) string {
	return `
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}-` + name + `
spec:
  host: "{{.Alias}}"
  trafficPolicy:
    loadBalancer:
      consistentHash:
        httpCookie: {name: pcs-` + name + `, ttl: 60s}
  subsets:
  - name: ` + version + `
    labels: {version: ` + version + `}
`
}

// caCertOrigination routes .Host on the shared gateway to the external echo's TLS port, originating
// TLS verified with the CA Secret .CA (caCertCredentialName).
func caCertOrigination() string {
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
  - {number: 80, name: http, protocol: HTTP, targetPort: 443}
  endpoints:
  - address: {{.External}}.{{.ExternalNamespace}}.svc.cluster.local
---
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}
spec:
  host: "{{.Name}}.{{.ExternalHost}}"
  trafficPolicy:
    tls:
      mode: SIMPLE
      caCertCredentialName: {{.CA}}
      sni: {{.TLSHost}}
---` + externalHostRoute
}

// someFromService checks that at least one response came from a pod of the service.
func someFromService(svc string) echo.Checker {
	return func(result echo.CallResult, err error) error {
		hosts := map[string]int{}
		for _, r := range result.Responses {
			if strings.HasPrefix(r.Hostname, svc+"-") {
				return nil
			}
			hosts[r.Hostname]++
		}
		return fmt.Errorf("expected at least one response from %q, got %v (err: %v)", svc, hosts, err)
	}
}

// responseHeaderNotContains checks that no response header value for key contains sub.
func responseHeaderNotContains(key, sub string) echo.Checker {
	return check.Each(func(r echoClient.Response) error {
		if v := strings.Join(r.ResponseHeaders.Values(key), ","); strings.Contains(v, sub) {
			return fmt.Errorf("expected response header %q not to contain %q, got %q", key, sub, v)
		}
		return nil
	})
}
