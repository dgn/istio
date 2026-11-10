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
	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
	"istio.io/istio/pkg/test/util/retry"
)

// Routing: VirtualService tcp/tls routes on gateways, http fault and retry details, how routing
// config for one host interacts with other config for it, and ServiceEntries as backends.
func init() {
	Register(
		// --- VirtualService tcp / tls on an Istio Gateway ---
		Feature{
			ID: "istioapi.virtualservice.tcp-subset-gateways",
			Description: "A VirtualService tcp rule whose match.gateways names another gateway is skipped on this one, " +
				"and the matching rule routes the connection to a DestinationRule subset.",
			Roles: ingressRole,
			Covers: []string{
				"VirtualService.tcp.match.gateways", "VirtualService.tcp.route.destination.subset",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: {{.Name}}-istio
spec:
  selector:
    gateway.networking.k8s.io/gateway-name: {{.GW}}
  servers:
  - port: {number: 9999, name: tcp, protocol: TCP}
    hosts: ["*"]
---
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}
spec:
  host: {{.Backend}}
  exportTo: ["."]
  subsets:
  - {name: v1, labels: {version: v1}}
  - {name: v2, labels: {version: v2}}
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["*"]
  gateways: ["{{.Name}}-istio"]
  tcp:
  - match: [{gateways: ["{{.Namespace}}/{{.Name}}-other"]}]
    route: [{destination: {host: {{.Backend}}, subset: v1, port: {number: 9090}}}]
  - match: [{gateways: ["{{.Name}}-istio"], port: 9999}]
    route: [{destination: {host: {{.Backend}}, subset: v2, port: {number: 9090}}}]
`, map[string]any{"GW": gw.Name})
				gw.Expect(t, Request{Address: gw.PodIP(t), Port: 9999, Scheme: scheme.TCP, Count: 10, Opts: newConnPerRequest}, Version("v2"))
			},
		},
		Feature{
			ID: "istioapi.virtualservice.tls-subset-weight-gateways",
			Description: "A VirtualService tls rule whose match.gateways names another gateway is skipped on this one, " +
				"and the matching rule's weighted routes send passthrough TLS only to the DestinationRule subset with a non-zero weight.",
			Roles: ingressRole,
			Covers: []string{
				"VirtualService.tls.match.gateways", "VirtualService.tls.route.destination.subset", "VirtualService.tls.route.weight",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				sni := s.Host("tls")
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: {{.Name}}-istio
spec:
  selector:
    gateway.networking.k8s.io/gateway-name: {{.GW}}
  servers:
  - port: {number: 9443, name: tls, protocol: TLS}
    hosts: ["{{.SNI}}"]
    tls: {mode: PASSTHROUGH}
---
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}
spec:
  host: {{.Backend}}
  exportTo: ["."]
  subsets:
  - {name: v1, labels: {version: v1}}
  - {name: v2, labels: {version: v2}}
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.SNI}}"]
  gateways: ["{{.Name}}-istio"]
  tls:
  - match: [{sniHosts: ["{{.SNI}}"], gateways: ["{{.Namespace}}/{{.Name}}-other"]}]
    route: [{destination: {host: {{.Backend}}, subset: v1, port: {number: 443}}}]
  - match: [{sniHosts: ["{{.SNI}}"], gateways: ["{{.Name}}-istio"]}]
    route:
    - {destination: {host: {{.Backend}}, subset: v1, port: {number: 443}}, weight: 0}
    - {destination: {host: {{.Backend}}, subset: v2, port: {number: 443}}, weight: 100}
`, map[string]any{"GW": gw.Name, "SNI": sni})
				// The backend's TLS port presents the echo test certificate, not one for this SNI.
				insecure := func(o *echo.CallOptions) {
					o.TLS.ServerName = sni
					o.TLS.InsecureSkipVerify = true
					o.NewConnectionPerRequest = true
				}
				gw.Expect(t, Request{Address: gw.PodIP(t), Host: sni, Port: 9443, Scheme: scheme.HTTPS, Count: 10, Opts: insecure}, Version("v2"))
			},
		},

		// --- http fault and retry details ---
		Feature{
			ID:          "istioapi.virtualservice.fault-grpc-status",
			Description: "VirtualService fault.abort.grpcStatus answers gRPC requests with that gRPC status instead of forwarding them.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.fault.abort.grpcStatus"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - match: [{uri: {prefix: /abort}}]
    fault:
      abort: {grpcStatus: UNAVAILABLE, percentage: {value: 100}}
    route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
  - match: [{uri: {prefix: /ok}}]
    route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
`))
				grpcLike := func(path string) Request {
					return Request{Path: path, Method: "POST", HTTP2: true, Headers: H("content-type", "application/grpc", "te", "trailers")}
				}
				tg.Expect(t, grpcLike("/ok"), check.And(check.OK(), fromBackend(true)))
				// UNAVAILABLE is gRPC status 14; the abort is a gRPC response, so the proxy (not the
				// backend) answers with HTTP 200 and the status in grpc-status.
				tg.Expect(t, grpcLike("/abort"), check.And(fromBackend(false), grpcStatus("14")))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.fault-delay-percentage",
			Description: "VirtualService fault.delay.percentage selects the share of requests to delay: 100 delays all of them, 0 none.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.fault.delay.percentage", "VirtualService.http.fault.delay.percentage.value"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				s.Apply(t, tg.VirtualService(`
  - match: [{uri: {prefix: /full}}]
    fault:
      delay: {fixedDelay: 2s, percentage: {value: 100}}
    route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
  - match: [{uri: {prefix: /zero}}]
    fault:
      delay: {fixedDelay: 2s, percentage: {value: 0}}
    route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
`))
				tg.Expect(t, Request{Path: "/zero"}, check.OK())
				// Two-sided, timed in one attempt: the 100% route is delayed (the config is in effect)
				// and the 0% route of the same VirtualService is not.
				retry.UntilSuccessOrFail(t, func() error {
					full, err := callDuration(t, tg, Request{Path: "/full"})
					if err != nil {
						return err
					}
					zero, err := callDuration(t, tg, Request{Path: "/zero"})
					if err != nil {
						return err
					}
					if full < 1800*time.Millisecond || zero > time.Second {
						return fmt.Errorf("100%% route took %v (want >= 2s), 0%% route took %v (want < 1s)", full, zero)
					}
					return nil
				}, retry.Timeout(time.Minute))
			},
		},
		Feature{
			ID:          "istioapi.virtualservice.retries-backoff",
			Description: "VirtualService retries.backoff waits between retries: a failing request with a 2s base backoff takes longer than without.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"VirtualService.http.retries.backoff"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				// 500, not 503: the default retry policy does not retry it, so only these retries do.
				s.Apply(t, tg.VirtualService(`
  - match: [{uri: {prefix: /backoff}}]
    retries: {attempts: 1, retryOn: 5xx, backoff: 2s}
    headers: {response: {set: {x-pcs-route: backoff}}}
    route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
  - match: [{uri: {prefix: /fast}}]
    retries: {attempts: 1, retryOn: 5xx}
    headers: {response: {set: {x-pcs-route: fast}}}
    route: [{destination: {host: {{.DestV1}}, port: {number: 80}}}]
`))
				tg.Expect(t, Request{Path: "/backoff?codes=500"}, check.And(check.Status(500), ResponseHeaderContains("x-pcs-route", "backoff")))
				tg.Expect(t, Request{Path: "/fast?codes=500"}, check.And(check.Status(500), ResponseHeaderContains("x-pcs-route", "fast")))
				// The wait before the single retry is random in [0, backoff), so compare sums over
				// several requests: 8 requests with a 2s base average 8s, and are below 3s with
				// probability < 0.1%; without backoff (Envoy's 25ms default) they take well under 3s.
				const n = 8
				retry.UntilSuccessOrFail(t, func() error {
					var backoff, fast time.Duration
					for range n {
						d, err := callDuration(t, tg, Request{Path: "/backoff?codes=500"})
						if err != nil {
							return err
						}
						backoff += d
						d, err = callDuration(t, tg, Request{Path: "/fast?codes=500"})
						if err != nil {
							return err
						}
						fast += d
					}
					if backoff < 3*time.Second || fast >= 3*time.Second {
						return fmt.Errorf("%d requests took %v with backoff (want >= 3s) and %v without (want < 3s)", n, backoff, fast)
					}
					return nil
				}, retry.Timeout(3*time.Minute))
			},
		},
		Feature{
			ID: "istioapi.virtualservice.retries-previous-hosts",
			Description: "VirtualService retries.retryIgnorePreviousHosts: with true a retry goes to another endpoint, so one bad endpoint " +
				"of two almost never fails a request; with false a retry may hit the same bad endpoint again.",
			Roles:      l7Roles,
			RequiresL7: true,
			Covers:     []string{"VirtualService.http.retries.retryIgnorePreviousHosts"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				// A good endpoint and the TCP echo port of another pod, which answers HTTP with
				// garbage (a 5xx the default retry policy does not retry). RANDOM picks either with
				// probability 1/2, independent of earlier picks.
				host := seBackend(t, s, tg, `
  location: MESH_EXTERNAL
  resolution: STATIC
  ports:
  - {number: 80, name: http, protocol: HTTP}
  endpoints:
  - {address: {{.GoodIP}}, ports: {http: 18080}}
  - {address: {{.BadIP}}, ports: {http: 19090}}
`, map[string]any{"GoodIP": s.Env.External[0].WorkloadsOrFail(t)[0].Address(), "BadIP": s.Env.Backend[0].WorkloadsOrFail(t)[0].Address()})
				s.Apply(t, destinationRule(`
    loadBalancer: {simple: RANDOM}
`), map[string]any{"PolicyHost": host})
				s.Apply(t, seRouteVirtualService(`
  - match: [{uri: {prefix: /ignore}}]
    retries: {attempts: 1, retryOn: 5xx, retryIgnorePreviousHosts: true}
    route: [{destination: {host: {{.SEHost}}, port: {number: 80}}}]
  - match: [{uri: {prefix: /same}}]
    retries: {attempts: 1, retryOn: 5xx, retryIgnorePreviousHosts: false}
    route: [{destination: {host: {{.SEHost}}, port: {number: 80}}}]
`))
				// With true, a request fails only if the first pick and all of Envoy's 5 re-picks
				// for the retry are the bad endpoint (~1.6%); with false, if both picks are (25%).
				// Of 60 requests that is <= 5 failures vs >= 6 with overwhelming probability.
				const n = 60
				tg.Expect(t, Request{Path: "/ignore", Count: n, Opts: newConnPerRequest}, MaxFailures(5))
				tg.Expect(t, Request{Path: "/same", Count: n, Opts: newConnPerRequest}, minFailures(6))
			},
		},

		// --- several routing configs for one host ---
		Feature{
			ID:          "istioapi.virtualservice.host-specificity",
			Description: "On a gateway, a request matching both a wildcard and an exact VirtualService host uses the exact host's routes.",
			Roles:       ingressRole,
			Covers:      []string{"interaction:virtualservice.host-specificity"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				// The wildcard VirtualService is created first, so age-based tie-breaking would favor it.
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: {{.Name}}-istio
spec:
  selector:
    gateway.networking.k8s.io/gateway-name: {{.GW}}
  servers:
  - port: {number: 80, name: http, protocol: HTTP}
    hosts: ["*.{{.Host}}"]
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}-wildcard
spec:
  hosts: ["*.{{.Host}}"]
  gateways: ["{{.Name}}-istio"]
  http:
  - route: [{destination: {host: {{.BackendV1}}, port: {number: 80}}}]
`, map[string]any{"GW": gw.Name})
				gw.Expect(t, Request{Host: s.Host("exact")}, Version("v1"))
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}-exact
spec:
  hosts: ["exact.{{.Host}}"]
  gateways: ["{{.Name}}-istio"]
  http:
  - route: [{destination: {host: {{.BackendV2}}, port: {number: 80}}}]
`)
				gw.Expect(t, Request{Host: s.Host("exact")}, Version("v2"))
				gw.ExpectStable(t, Request{Host: s.Host("other")}, Version("v1"))
			},
		},
		Feature{
			ID: "istioapi.gateway-api.httproute-and-virtualservice",
			Description: "An HTTPRoute (on a Gateway API Gateway) and a VirtualService (on an Istio Gateway selecting the same pods) " +
				"for the same host and port are merged: each one's rules route their own paths.",
			Roles:  ingressRole,
			Covers: []string{"interaction:gatewayapi.route-vs-virtualservice"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone})
				s.Apply(t, tg.HTTPRoute(`
  - matches: [{path: {type: PathPrefix, value: /httproute}}]
    backendRefs:
    - {name: {{.DestV1}}, port: 80}
`))
				tg.Expect(t, Request{Path: "/httproute"}, Version("v1"))
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: {{.Name}}-istio
spec:
  selector:
    gateway.networking.k8s.io/gateway-name: {{.GW}}
  servers:
  - port: {number: 80, name: http-istio, protocol: HTTP}
    hosts: ["{{.TargetHost}}"]
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.TargetHost}}"]
  gateways: ["{{.Name}}-istio"]
  http:
  - match: [{uri: {prefix: /virtualservice}}]
    route: [{destination: {host: {{.DestV2}}, port: {number: 80}}}]
`, map[string]any{"GW": tg.Gateway().Name})
				tg.Expect(t, Request{Path: "/virtualservice"}, Version("v2"))
				tg.ExpectStable(t, Request{Path: "/httproute"}, Version("v1"))
			},
		},

		// --- ServiceEntry as a backend ---
		Feature{
			ID:          "backend.serviceentry.endpoint-weight",
			Description: "ServiceEntry endpoints.weight splits traffic between the endpoints of a STATIC ServiceEntry in proportion.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers: []string{
				"ServiceEntry.endpoints.weight", "ServiceEntry.endpoints.address", "ServiceEntry.location", "ServiceEntry.location=MESH_EXTERNAL",
				"ServiceEntry.resolution", "ServiceEntry.ports.name", "ServiceEntry.ports.number", "ServiceEntry.ports.protocol",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone})
				// The external echo reports version v1, the backend's v2 pod v2.
				tg.ServiceEntry(t, `
  location: MESH_EXTERNAL
  resolution: STATIC
  ports:
  - {number: 80, name: http, protocol: HTTP}
  endpoints:
  - {address: {{.V1IP}}, ports: {http: 18080}, weight: 1}
  - {address: {{.V2IP}}, ports: {http: 18080}, weight: 4}
`, map[string]any{"V1IP": s.Env.External[0].WorkloadsOrFail(t)[0].Address(), "V2IP": backendPodIP(t, s, "v2")})
				tg.Expect(t, Request{Count: 10}, check.And(someVersion("v1"), someVersion("v2")))
				// One request at a time: with concurrent requests a least-request balancer also weighs
				// endpoints by their outstanding requests, which flattens the configured split.
				retry.UntilSuccessOrFail(t, func() error {
					var res echo.CallResult
					for range SampleSize(20, 80) {
						r, err := tg.Call(t, Request{})
						if err != nil {
							return err
						}
						res.Responses = append(res.Responses, r.Responses...)
					}
					return Distribution(map[string]int{"v1": 20, "v2": 80})(res, nil)
				}, retry.Timeout(3*time.Minute))
			},
		},
		Feature{
			ID:          "backend.serviceentry.endpoint-labels-subset",
			Description: "A DestinationRule subset of a ServiceEntry host selects the endpoints whose ServiceEntry endpoints.labels match.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"ServiceEntry.endpoints.labels"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteVirtualService})
				host := seBackend(t, s, tg, `
  location: MESH_EXTERNAL
  resolution: STATIC
  ports:
  - {number: 80, name: http, protocol: HTTP}
  endpoints:
  - {address: {{.V1IP}}, ports: {http: 18080}, labels: {pcs-subset: a}}
  - {address: {{.V2IP}}, ports: {http: 18080}, labels: {pcs-subset: b}}
`, map[string]any{"V1IP": s.Env.External[0].WorkloadsOrFail(t)[0].Address(), "V2IP": backendPodIP(t, s, "v2")})
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}
spec:
  host: {{.PolicyHost}}
  subsets:
  - {name: a, labels: {pcs-subset: a}}
  - {name: b, labels: {pcs-subset: b}}
`, map[string]any{"PolicyHost": host})
				s.Apply(t, seRouteVirtualService(`
  - match: [{uri: {prefix: /a}}]
    route: [{destination: {host: {{.SEHost}}, subset: a, port: {number: 80}}}]
  - route: [{destination: {host: {{.SEHost}}, subset: b, port: {number: 80}}}]
`))
				tg.Expect(t, Request{Path: "/a", Count: 10, Opts: newConnPerRequest}, Version("v1"))
				tg.Expect(t, Request{Path: "/b", Count: 10, Opts: newConnPerRequest}, Version("v2"))
			},
		},
		Feature{
			ID: "backend.serviceentry.endpoint-identity",
			Description: "The gateway verifies the mTLS identity of MESH_INTERNAL ServiceEntry endpoints against endpoints.serviceAccount " +
				"or, without one, the ServiceEntry's subjectAltNames: a matching identity is reached, a mismatching one is refused.",
			Roles: ingressRole,
			Covers: []string{
				"ServiceEntry.endpoints.serviceAccount", "ServiceEntry.subjectAltNames", "ServiceEntry.endpoints.labels",
				"ServiceEntry.location=MESH_INTERNAL",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone})
				ip := backendPodIP(t, s, "v1")
				sa := "spiffe://cluster.local/ns/" + s.Namespace() + "/sa/default"
				wrong := "spiffe://cluster.local/ns/" + s.Namespace() + "/sa/pcs-wrong"
				// Each ServiceEntry gets its own host and path prefix. An endpoint serviceAccount
				// (or the security.istio.io/tlsMode label) makes the gateway use Istio mTLS.
				ses := []struct{ name, endpoint, sans string }{
					{"sa-ok", `{address: ` + ip + `, ports: {http: 18080}, serviceAccount: default}`, ""},
					{"san-ok", `{address: ` + ip + `, ports: {http: 18080}, labels: {security.istio.io/tlsMode: istio}}`, `["` + sa + `"]`},
					{"sa-bad", `{address: ` + ip + `, ports: {http: 18080}, serviceAccount: pcs-wrong}`, ""},
					{"san-bad", `{address: ` + ip + `, ports: {http: 18080}, labels: {security.istio.io/tlsMode: istio}}`, `["` + wrong + `"]`},
				}
				var rules strings.Builder
				for _, se := range ses {
					sans := ""
					if se.sans != "" {
						sans = "\n  subjectAltNames: " + se.sans
					}
					host := se.name + "." + s.Host()
					s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}-{{.SE}}
spec:
  hosts: ["{{.SEHost}}"]
  location: MESH_INTERNAL
  resolution: STATIC
  ports:
  - {number: 80, name: http, protocol: HTTP}
  endpoints:
  - {{.Endpoint}}`+sans+`
`, map[string]any{"SE": se.name, "SEHost": host, "Endpoint": se.endpoint})
					fmt.Fprintf(&rules, `
  - matches: [{path: {type: PathPrefix, value: /%s}}]
    backendRefs:
    - {group: networking.istio.io, kind: Hostname, name: %s, port: 80}
`, se.name, host)
				}
				s.Apply(t, tg.HTTPRoute(rules.String()))
				// The refused cases show that the identity is verified, so the gateway used Istio mTLS
				// (the backend also accepts plaintext).
				tg.Expect(t, Request{Path: "/sa-ok"}, Version("v1"))
				tg.Expect(t, Request{Path: "/san-ok"}, Version("v1"))
				tg.ExpectStable(t, Request{Path: "/sa-bad"}, check.Status(503))
				tg.ExpectStable(t, Request{Path: "/san-bad"}, check.Status(503))
			},
		},
		Feature{
			ID: "backend.serviceentry.same-host-as-service",
			Description: "A ServiceEntry with the same hostname (and namespace) as a Kubernetes Service does not take it over: " +
				"routes to the Service keep reaching its pods, not the ServiceEntry's endpoints.",
			Roles:  ingressRole,
			Covers: []string{"interaction:serviceentry.same-host-as-service"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone})
				s.Apply(t, `
apiVersion: v1
kind: Service
metadata:
  name: {{.Name}}
spec:
  selector: {app: {{.Backend}}, version: v1}
  ports:
  - {name: http, port: 80, targetPort: 18080}
`)
				s.Apply(t, tg.HTTPRoute(`
  - backendRefs:
    - {name: {{.Name}}, port: 80}
`))
				tg.Expect(t, Request{Count: 5}, check.And(Version("v1"), FromService(BackendSvc)))
				s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}-squat
spec:
  hosts: ["{{.Name}}.{{.Namespace}}.svc.cluster.local"]
  location: MESH_INTERNAL
  resolution: STATIC
  ports:
  - {number: 80, name: http, protocol: HTTP}
  endpoints:
  - {address: {{.IP}}, ports: {http: 18080}}
`, map[string]any{"IP": s.Env.External[0].WorkloadsOrFail(t)[0].Address()})
				tg.ExpectStable(t, Request{Count: 5, Opts: newConnPerRequest}, FromService(BackendSvc))
			},
		},
		Feature{
			ID:          "backend.kubernetes-externalname",
			Description: "A route to a Kubernetes ExternalName Service reaches the service it names.",
			Roles:       ingressRole,
			Covers:      []string{"interaction:serviceentry.kubernetes-externalname"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone})
				s.Apply(t, `
apiVersion: v1
kind: Service
metadata:
  name: {{.Name}}
spec:
  type: ExternalName
  externalName: {{.External}}.{{.ExternalNamespace}}.svc.cluster.local
  ports:
  - {name: http, port: 80, targetPort: 18080}
`)
				s.Apply(t, tg.HTTPRoute(`
  - backendRefs:
    - {name: {{.Name}}, port: 80}
`))
				tg.Expect(t, Request{}, FromService(ExternalSvc))
			},
		},

		// --- Behavior-changing istiod flags ---
		Feature{
			ID: "flag.persistent-session",
			Description: "With PILOT_ENABLE_PERSISTENT_SESSION_FILTER, a Service labeled with PILOT_PERSISTENT_SESSION_LABEL " +
				"(cookie) or PILOT_PERSISTENT_SESSION_HEADER_LABEL (header) gets sticky sessions: the proxy returns a session " +
				"cookie or header, and requests carrying it keep reaching the same endpoint.",
			Roles: ingressRole,
			Covers: []string{
				"flag:istiod:PILOT_ENABLE_PERSISTENT_SESSION_FILTER",
				"flag:istiod:PILOT_PERSISTENT_SESSION_LABEL",
				"flag:istiod:PILOT_PERSISTENT_SESSION_HEADER_LABEL",
			},
			IstiodEnv: map[string]string{
				"PILOT_ENABLE_PERSISTENT_SESSION_FILTER": "true",
				"PILOT_PERSISTENT_SESSION_LABEL":         persistentSessionCookieLabel,
				"PILOT_PERSISTENT_SESSION_HEADER_LABEL":  persistentSessionHeaderLabel,
			},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone})
				// Both Services select every backend version, so without stickiness requests spread over both.
				s.Apply(t, `
apiVersion: v1
kind: Service
metadata:
  name: {{.Name}}-cookie
  labels: {`+persistentSessionCookieLabel+`: pcs-session}
spec:
  selector: {app: {{.Backend}}}
  ports:
  - {name: http, port: 80, targetPort: 18080}
---
apiVersion: v1
kind: Service
metadata:
  name: {{.Name}}-header
  labels: {`+persistentSessionHeaderLabel+`: x-pcs-session}
spec:
  selector: {app: {{.Backend}}}
  ports:
  - {name: http, port: 80, targetPort: 18080}
`)
				s.Apply(t, tg.HTTPRoute(`
  - matches: [{path: {type: PathPrefix, value: /cookie}}]
    backendRefs:
    - {name: {{.Name}}-cookie, port: 80}
  - matches: [{path: {type: PathPrefix, value: /header}}]
    backendRefs:
    - {name: {{.Name}}-header, port: 80}
`))
				for _, path := range []string{"/cookie", "/header"} {
					tg.Expect(t, Request{Path: path, Count: 20, Opts: newConnPerRequest}, check.And(check.OK(), someVersion("v1"), someVersion("v2")))
				}
				expectSticky := func(path, header string, session func(echoClient.Response) string) {
					retry.UntilSuccessOrFail(t, func() error {
						res, err := tg.Call(t, Request{Path: path})
						if err != nil {
							return err
						}
						if len(res.Responses) == 0 || res.Responses[0].Code != "200" {
							return fmt.Errorf("%s: unexpected first response %v", path, res.Responses)
						}
						first := res.Responses[0]
						value := session(first)
						if value == "" {
							return fmt.Errorf("%s: no session returned (response headers %v)", path, first.ResponseHeaders)
						}
						res, err = tg.Call(t, Request{Path: path, Count: 20, Headers: H(header, value), Opts: newConnPerRequest})
						if err != nil {
							return err
						}
						return check.And(check.OK(), check.Each(func(r echoClient.Response) error {
							if r.Hostname != first.Hostname {
								return fmt.Errorf("%s: session for %s reached %s", path, first.Hostname, r.Hostname)
							}
							return nil
						}))(res, nil)
					}, retry.Timeout(2*time.Minute), retry.Delay(time.Second))
				}
				expectSticky("/cookie", "Cookie", func(r echoClient.Response) string {
					for _, c := range r.ResponseHeaders.Values("Set-Cookie") {
						if pair, _, _ := strings.Cut(c, ";"); strings.HasPrefix(pair, "pcs-session=") {
							return pair
						}
					}
					return ""
				})
				expectSticky("/header", "x-pcs-session", func(r echoClient.Response) string {
					return r.ResponseHeaders.Get("x-pcs-session")
				})
			},
		},
		Feature{
			ID: "flag.100-continue-disabled",
			Description: "With ENABLE_100_CONTINUE_HEADERS=false the proxy answers Expect: 100-continue itself " +
				"and does not forward the Expect header to the backend.",
			Roles:      l7Roles,
			RequiresL7: true,
			Covers:     []string{"flag:istiod:ENABLE_100_CONTINUE_HEADERS"},
			IstiodEnv:  map[string]string{"ENABLE_100_CONTINUE_HEADERS": "false"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				tg.Expect(t, Request{Headers: H("Expect", "100-continue")}, check.And(check.OK(), RequestHeaderAbsent("Expect")))
			},
		},
	)
}

// seBackend applies a ServiceEntry (spec as for Target.ServiceEntry) that a VirtualService from
// seRouteVirtualService routes to, and returns its host (.SEHost). Create the Target with
// RouteVirtualService. Ingress: the VirtualService routes the target host to it. Workload roles:
// requests carry the ServiceEntry host (see Target.ServiceEntry) and the VirtualService is for it.
func seBackend(t framework.TestContext, s *Scenario, tg *Target, spec string, extra ...map[string]any) string {
	t.Helper()
	if tg.Gateway() == nil {
		host := tg.ServiceEntry(t, spec, extra...)
		s.setTargetVars(map[string]any{"VSHost": host})
		return host
	}
	host := s.Name() + "." + ExternalHost
	s.setTargetVars(map[string]any{"SEHost": host, "VSHost": tg.Host()})
	s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}-se
spec:
  hosts: ["{{.SEHost}}"]
`+spec, extra...)
	return host
}

// seRouteVirtualService is a VirtualService template (http items) for the host seBackend set up.
func seRouteVirtualService(http string) string {
	return `
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}
spec:
  hosts: ["{{.VSHost}}"]
  gateways: ["{{.TargetGateway}}"]
  http:
` + http
}

// backendPodIP is the address of a pod of the shared backend with the given version.
func backendPodIP(t framework.TestContext, s *Scenario, version string) string {
	t.Helper()
	for _, inst := range s.Env.Backend {
		for _, w := range inst.WorkloadsOrFail(t) {
			if strings.HasPrefix(w.PodName(), BackendSvc+"-"+version+"-") {
				return w.Address()
			}
		}
	}
	t.Fatalf("no %s pod of %s", version, BackendSvc)
	return ""
}

func callDuration(t framework.TestContext, tg *Target, r Request) (time.Duration, error) {
	start := time.Now()
	_, err := tg.Call(t, r)
	return time.Since(start), err
}

// Service labels naming the persistent session cookie and header (values of
// PILOT_PERSISTENT_SESSION_LABEL and PILOT_PERSISTENT_SESSION_HEADER_LABEL for flag.persistent-session).
const (
	persistentSessionCookieLabel = "pcs.istio.io/session-cookie"
	persistentSessionHeaderLabel = "pcs.istio.io/session-header"
)

// grpcStatus checks the grpc-status of every response (in headers, as for a trailers-only response).
func grpcStatus(code string) echo.Checker {
	return check.Each(func(r echoClient.Response) error {
		if got := r.ResponseHeaders.Get("grpc-status"); got != code {
			return fmt.Errorf("expected grpc-status %q, got %q (code %s)", code, got, r.Code)
		}
		return nil
	})
}

// minFailures checks that at least n responses were not 200.
func minFailures(n int) echo.Checker {
	return func(result echo.CallResult, err error) error {
		failed := 0
		for _, r := range result.Responses {
			if r.Code != "200" {
				failed++
			}
		}
		if len(result.Responses) == 0 || failed < n {
			return fmt.Errorf("expected at least %d failures, got %d of %d (err: %v)", n, failed, len(result.Responses), err)
		}
		return nil
	}
}
