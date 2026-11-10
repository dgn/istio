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
	"time"

	"google.golang.org/protobuf/types/known/wrapperspb"

	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
	"istio.io/istio/pkg/test/framework/components/namespace"
)

// Mesh-wide settings seen from an ingress gateway: path normalization, cluster connect timeout,
// the default retry policy's per-try timeout, the default exportTo of Services, VirtualServices and
// DestinationRules, ServiceEntry visibility, the root namespace and discovery selectors (MeshConfig,
// run serially); plus per-gateway ProxyConfig connectionSettings timeouts and profiles
// (proxy.istio.io/config, run in parallel).
func init() {
	Register(
		// ---- MeshConfig.pathNormalization ----
		Feature{
			ID:          "meshconfig.path-normalization-none",
			Description: "MeshConfig pathNormalization NONE forwards the request path unchanged: dot segments reach the backend.",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers:      []string{"MeshConfig.pathNormalization", "MeshConfig.pathNormalization.normalization", "MeshConfig.pathNormalization.normalization=NONE"},
			MeshConfig: `
pathNormalization:
  normalization: NONE
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := pathNormalizationGateway(t, s)
				// By default (BASE) the backend sees /a.
				gw.Expect(t, Request{Path: "/x/../a"}, Path("/x/../a"))
			},
		},
		Feature{
			ID:          "meshconfig.path-normalization-base",
			Description: "MeshConfig pathNormalization BASE resolves dot segments but keeps repeated slashes.",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers:      []string{"MeshConfig.pathNormalization.normalization=BASE"},
			NoControl:   "BASE is the behavior of the default (unset) pathNormalization, so removing the setting changes nothing observable",
			MeshConfig: `
pathNormalization:
  normalization: BASE
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := pathNormalizationGateway(t, s)
				gw.Expect(t, Request{Path: "/x/../a"}, Path("/a"))
				gw.Expect(t, Request{Path: "/a//b"}, Path("/a//b"))
			},
		},
		Feature{
			ID:          "meshconfig.path-normalization-default",
			Description: "MeshConfig pathNormalization DEFAULT behaves as BASE: dot segments are resolved, repeated slashes kept.",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers:      []string{"MeshConfig.pathNormalization.normalization=DEFAULT"},
			NoControl:   "DEFAULT is the behavior of the unset pathNormalization, so removing the setting changes nothing observable",
			MeshConfig: `
pathNormalization:
  normalization: DEFAULT
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := pathNormalizationGateway(t, s)
				gw.Expect(t, Request{Path: "/x/../a"}, Path("/a"))
				gw.Expect(t, Request{Path: "/a//b"}, Path("/a//b"))
			},
		},
		Feature{
			ID:          "meshconfig.path-normalization-merge-slashes",
			Description: "MeshConfig pathNormalization MERGE_SLASHES merges repeated slashes (and resolves dot segments); escaped slashes are kept.",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers:      []string{"MeshConfig.pathNormalization.normalization=MERGE_SLASHES"},
			MeshConfig: `
pathNormalization:
  normalization: MERGE_SLASHES
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := pathNormalizationGateway(t, s)
				gw.Expect(t, Request{Path: "/a//b"}, Path("/a/b"))
				gw.Expect(t, Request{Path: "/x/../a%2Fb"}, Path("/a%2Fb"))
			},
		},
		Feature{
			ID:          "meshconfig.path-normalization-decode-and-merge-slashes",
			Description: "MeshConfig pathNormalization DECODE_AND_MERGE_SLASHES decodes escaped slashes and merges repeated slashes.",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers:      []string{"MeshConfig.pathNormalization.normalization=DECODE_AND_MERGE_SLASHES"},
			MeshConfig: `
pathNormalization:
  normalization: DECODE_AND_MERGE_SLASHES
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := pathNormalizationGateway(t, s)
				gw.Expect(t, Request{Path: "/a%2Fb//c"}, Path("/a/b/c"))
			},
		},

		// ---- MeshConfig.connectTimeout / defaultHttpRetryPolicy ----
		Feature{
			ID:          "meshconfig.connect-timeout",
			Description: "MeshConfig connectTimeout bounds upstream connection attempts: a request to an unreachable endpoint fails after about the timeout.",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers:      []string{"MeshConfig.connectTimeout"},
			// The default is 10s per attempt; the installed default retry policy retries connect
			// failures twice, so the request fails after about 3x the timeout.
			MeshConfig: `
connectTimeout: 1s
`,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{Routing: RouteNone})
				// 192.0.2.1 (TEST-NET-1) is never assigned: connection attempts time out.
				tg.ServiceEntry(t, `
  location: MESH_EXTERNAL
  resolution: STATIC
  ports:
  - {number: 80, name: http, protocol: HTTP}
  endpoints:
  - address: 192.0.2.1
`)
				r := Request{Opts: func(o *echo.CallOptions) { o.Timeout = 15 * time.Second }}
				// Wait for the route: until then the gateway answers 404 at once.
				tg.Expect(t, r, statusIn(502, 503, 504))
				timed(t, tg, r, statusIn(502, 503, 504), time.Second, 9*time.Second)
			},
		},
		Feature{
			ID: "meshconfig.default-http-retry-policy-per-try-timeout",
			Description: "MeshConfig defaultHttpRetryPolicy perTryTimeout bounds each attempt of routes without their own retry policy: " +
				"a slow backend fails with 504.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"MeshConfig.defaultHttpRetryPolicy.perTryTimeout"},
			MeshConfig: `
defaultHttpRetryPolicy:
  attempts: 2
  retryOn: gateway-error
  perTryTimeout: 500ms
`,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				tg.Expect(t, Request{Path: "/?delay=10ms"}, check.OK())
				// Without a per-try (or route) timeout the request succeeds after 3s.
				tg.Expect(t, Request{Path: "/?delay=3s"}, check.Status(504))
			},
		},

		// ---- default exportTo ----
		Feature{
			ID: "meshconfig.default-virtual-service-export-to",
			Description: "MeshConfig defaultVirtualServiceExportTo [.] keeps VirtualServices without exportTo private to their namespace: " +
				"a gateway does not use one bound to it from another namespace, but uses an explicitly exported one.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"MeshConfig.defaultVirtualServiceExportTo"},
			MeshConfig: `
defaultVirtualServiceExportTo: ["."]
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				vars := map[string]any{"GW": gw.Name}
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
    hosts: ["*/*.{{.Host}}"]
`, vars)
				vs := func(id, gateway, exportTo string) string {
					return `
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: {{.Name}}-` + id + `
spec:
  hosts: ["` + id + `.{{.Host}}"]
  gateways: ["` + gateway + `"]` + exportTo + `
  http:
  - route: [{destination: {host: "{{.BackendV1}}.{{.Namespace}}.svc.cluster.local", port: {number: 80}}}]
`
				}
				s.ApplyTo(t, s.Env.ExternalNamespace.Name(), vs("private", "{{.Namespace}}/{{.Name}}-istio", ""))
				s.Apply(t, vs("local", "{{.Name}}-istio", ""))
				s.ApplyTo(t, s.Env.ExternalNamespace.Name(), vs("exported", "{{.Namespace}}/{{.Name}}-istio", `
  exportTo: ["*"]`))
				gw.Expect(t, Request{Host: "local." + s.Host()}, Version("v1"))
				// Witness: a VirtualService from the other namespace reaches the gateway.
				gw.Expect(t, Request{Host: "exported." + s.Host()}, Version("v1"))
				gw.ExpectStable(t, Request{Host: "private." + s.Host()}, check.Status(404))
			},
		},
		Feature{
			ID: "meshconfig.default-service-export-to",
			Description: "MeshConfig defaultServiceExportTo [.] keeps services without exportTo private to their namespace: " +
				"a gateway cannot route to one from another namespace, but can to an explicitly exported one.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"MeshConfig.defaultServiceExportTo"},
			MeshConfig: `
defaultServiceExportTo: ["."]
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				ext := s.Env.ExternalNamespace.Name()
				private := routeToServiceEntryIn(t, s, gw, ext, "private", "")
				local := routeToServiceEntryIn(t, s, gw, s.Namespace(), "local", "")
				exported := routeToServiceEntryIn(t, s, gw, ext, "exported", `  exportTo: ["*"]`)
				gw.Expect(t, Request{Host: local}, FromService(ExternalSvc))
				gw.Expect(t, Request{Host: exported}, FromService(ExternalSvc))
				gw.ExpectStable(t, Request{Host: private}, notServed())
			},
		},
		Feature{
			ID: "meshconfig.default-destination-rule-export-to",
			Description: "MeshConfig defaultDestinationRuleExportTo [.] keeps DestinationRules without exportTo private to their namespace: " +
				"a gateway in another namespace ignores them, but applies an explicitly exported one.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"MeshConfig.defaultDestinationRuleExportTo"},
			MeshConfig: `
defaultDestinationRuleExportTo: ["."]
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				ext := s.Env.ExternalNamespace.Name()
				// The ServiceEntries and DestinationRules live in the same (other) namespace: a proxy only
				// looks up DestinationRules in its own, the service's and the root namespace. TLS
				// origination to the plaintext backend breaks requests wherever the rule applies.
				private := routeToServiceEntryIn(t, s, gw, ext, "private", "")
				exported := routeToServiceEntryIn(t, s, gw, ext, "exported", "")
				// Routes must work first, so the witness below cannot pass on a not-yet-ready route.
				gw.Expect(t, Request{Host: private}, FromService(ExternalSvc))
				gw.Expect(t, Request{Host: exported}, FromService(ExternalSvc))
				s.ApplyTo(t, ext, tlsOriginationRule("private", ""))
				s.ApplyTo(t, ext, tlsOriginationRule("exported", `
  exportTo: ["*"]`))
				// Witness: a DestinationRule from the other namespace reaches the gateway.
				gw.Expect(t, Request{Host: exported}, notServed())
				gw.ExpectStable(t, Request{Host: private}, FromService(ExternalSvc))
			},
		},

		// ---- MeshConfig.serviceEntryVisibility ----
		Feature{
			ID: "meshconfig.service-entry-visibility-namespace",
			Description: "MeshConfig serviceEntryVisibility defaultVisibility NAMESPACE (applyToSidecars) restricts ServiceEntries to their namespace: " +
				"a gateway routes to one in its own namespace but not to one in another namespace, unless a PUBLIC policy matches that namespace.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers: []string{
				"MeshConfig.serviceEntryVisibility", "MeshConfig.serviceEntryVisibility.applyToSidecars",
				"MeshConfig.serviceEntryVisibility.defaultVisibility", "MeshConfig.serviceEntryVisibility.defaultVisibility=NAMESPACE",
			},
			MeshConfig: `
serviceEntryVisibility:
  applyToSidecars: true
  defaultVisibility: NAMESPACE
  policies:
  - visibility: PUBLIC
    matchingRules:
    - namespaceSelector:
        matchLabels: {kubernetes.io/metadata.name: {{.SystemNamespace}}}
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				// Applied before the visible ones, so it has reached istiod when they work.
				remote := routeToServiceEntryIn(t, s, gw, s.Env.ExternalNamespace.Name(), "remote", `  exportTo: ["*"]`)
				local := routeToServiceEntryIn(t, s, gw, s.Namespace(), "local", "")
				// Witness: the gateway can reach a ServiceEntry in another namespace when it is public.
				public := routeToServiceEntryIn(t, s, gw, s.Env.SystemNamespace, "public", `  exportTo: ["*"]`)
				gw.Expect(t, Request{Host: local}, FromService(ExternalSvc))
				gw.Expect(t, Request{Host: public}, FromService(ExternalSvc))
				gw.ExpectStable(t, Request{Host: remote}, notServed())
			},
		},
		Feature{
			ID: "meshconfig.service-entry-visibility-public-policy",
			Description: "MeshConfig serviceEntryVisibility policies override defaultVisibility NONE by namespace labels: " +
				"a PUBLIC policy (matchLabels) exposes ServiceEntries of the matching namespace, others stay hidden even in their own namespace.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers: []string{
				"MeshConfig.serviceEntryVisibility.defaultVisibility=NONE", "MeshConfig.serviceEntryVisibility.policies",
				"MeshConfig.serviceEntryVisibility.policies.matchingRules", "MeshConfig.serviceEntryVisibility.policies.matchingRules.namespaceSelector",
				"MeshConfig.serviceEntryVisibility.policies.matchingRules.namespaceSelector.matchLabels",
				"MeshConfig.serviceEntryVisibility.policies.visibility", "MeshConfig.serviceEntryVisibility.policies.visibility=PUBLIC",
			},
			MeshConfig: `
serviceEntryVisibility:
  applyToSidecars: true
  defaultVisibility: NONE
  policies:
  - visibility: PUBLIC
    matchingRules:
    - namespaceSelector:
        matchLabels: {kubernetes.io/metadata.name: {{.ExternalNamespace}}}
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				local := routeToServiceEntryIn(t, s, gw, s.Namespace(), "local", "")
				public := routeToServiceEntryIn(t, s, gw, s.Env.ExternalNamespace.Name(), "public", "")
				gw.Expect(t, Request{Host: public}, FromService(ExternalSvc))
				gw.ExpectStable(t, Request{Host: local}, notServed())
			},
		},
		Feature{
			ID: "meshconfig.service-entry-visibility-none-policy",
			Description: "MeshConfig serviceEntryVisibility policy NONE (matchExpressions) hides the ServiceEntries of matching namespaces, " +
				"even from their own namespace, while defaultVisibility PUBLIC keeps the others visible.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers: []string{
				"MeshConfig.serviceEntryVisibility.defaultVisibility=PUBLIC", "MeshConfig.serviceEntryVisibility.policies.visibility=NONE",
				"MeshConfig.serviceEntryVisibility.policies.matchingRules.namespaceSelector.matchExpressions",
				"MeshConfig.serviceEntryVisibility.policies.matchingRules.namespaceSelector.matchExpressions.key",
				"MeshConfig.serviceEntryVisibility.policies.matchingRules.namespaceSelector.matchExpressions.operator",
				"MeshConfig.serviceEntryVisibility.policies.matchingRules.namespaceSelector.matchExpressions.values",
			},
			MeshConfig: `
serviceEntryVisibility:
  applyToSidecars: true
  defaultVisibility: PUBLIC
  policies:
  - visibility: NONE
    matchingRules:
    - namespaceSelector:
        matchExpressions:
        - {key: kubernetes.io/metadata.name, operator: In, values: ["{{.Namespace}}"]}
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				local := routeToServiceEntryIn(t, s, gw, s.Namespace(), "local", "")
				public := routeToServiceEntryIn(t, s, gw, s.Env.ExternalNamespace.Name(), "public", "")
				gw.Expect(t, Request{Host: public}, FromService(ExternalSvc))
				gw.ExpectStable(t, Request{Host: local}, notServed())
			},
		},
		Feature{
			ID: "meshconfig.service-entry-visibility-namespace-policy",
			Description: "MeshConfig serviceEntryVisibility policy NAMESPACE restricts the ServiceEntries of matching namespaces to their namespace, " +
				"even if exported to all.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"MeshConfig.serviceEntryVisibility.policies.visibility=NAMESPACE"},
			MeshConfig: `
serviceEntryVisibility:
  applyToSidecars: true
  policies:
  - visibility: NAMESPACE
    matchingRules:
    - namespaceSelector:
        matchLabels: {kubernetes.io/metadata.name: {{.ExternalNamespace}}}
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				// Applied before the visible one, so it has reached istiod when the visible one works.
				remote := routeToServiceEntryIn(t, s, gw, s.Env.ExternalNamespace.Name(), "remote", `  exportTo: ["*"]`)
				local := routeToServiceEntryIn(t, s, gw, s.Namespace(), "local", "")
				// Witness: a ServiceEntry of a namespace the policy does not match stays exported.
				other := routeToServiceEntryIn(t, s, gw, s.Env.SystemNamespace, "other", `  exportTo: ["*"]`)
				gw.Expect(t, Request{Host: local}, FromService(ExternalSvc))
				gw.Expect(t, Request{Host: other}, FromService(ExternalSvc))
				gw.ExpectStable(t, Request{Host: remote}, notServed())
			},
		},

		// ---- MeshConfig.rootNamespace and discoverySelectors ----
		Feature{
			ID: "meshconfig.root-namespace",
			Description: "MeshConfig rootNamespace selects the namespace whose exported DestinationRules apply mesh-wide: " +
				"a gateway applies a rule from the configured root namespace to a service in another namespace, and no longer one from the default root.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"MeshConfig.rootNamespace"},
			MeshConfig: `
rootNamespace: {{.ExternalNamespace}}
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				// The services live in the gateway's namespace: a DestinationRule in a third namespace
				// applies to them only as a root namespace rule.
				configured := routeToServiceEntryIn(t, s, gw, s.Namespace(), "configured", "")
				previous := routeToServiceEntryIn(t, s, gw, s.Namespace(), "previous", "")
				// Routes must work first, so the witness below cannot pass on a not-yet-ready route.
				gw.Expect(t, Request{Host: configured}, FromService(ExternalSvc))
				gw.Expect(t, Request{Host: previous}, FromService(ExternalSvc))
				s.ApplyTo(t, s.Env.SystemNamespace, tlsOriginationRule("previous", ""))
				s.ApplyTo(t, s.Env.ExternalNamespace.Name(), tlsOriginationRule("configured", ""))
				gw.Expect(t, Request{Host: configured}, notServed())
				gw.ExpectStable(t, Request{Host: previous}, FromService(ExternalSvc))
			},
		},
		Feature{
			ID: "meshconfig.discovery-selectors",
			Description: "MeshConfig discoverySelectors limit the namespaces istiod reads config from: " +
				"a ServiceEntry in a namespace no selector matches is unknown to the gateway, one matched by any selector is used.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers: []string{
				"MeshConfig.discoverySelectors", "MeshConfig.discoverySelectors.matchExpressions",
				"MeshConfig.discoverySelectors.matchExpressions.key", "MeshConfig.discoverySelectors.matchExpressions.operator",
				"MeshConfig.discoverySelectors.matchExpressions.values", "MeshConfig.discoverySelectors.matchLabels",
			},
			// Replaces the installed selectors: the first selector keeps every namespace the test framework
			// discovers (all but those labeled istio.io/test-exclude-namespace) except the feature's own
			// labeled ones, the second re-includes one of them by label.
			MeshConfig: `
discoverySelectors:
- matchExpressions:
  - {key: istio.io/test-exclude-namespace, operator: DoesNotExist}
  - {key: pcs.istio.io/undiscovered, operator: NotIn, values: ["{{.Name}}"]}
- matchLabels: {pcs.istio.io/discovered: "{{.Name}}"}
`,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				hidden := namespace.NewOrFail(t, namespace.Config{
					Prefix: "pcs-hidden",
					Labels: map[string]string{"pcs.istio.io/undiscovered": s.Name()},
				})
				selected := namespace.NewOrFail(t, namespace.Config{
					Prefix: "pcs-selected",
					Labels: map[string]string{"pcs.istio.io/undiscovered": s.Name(), "pcs.istio.io/discovered": s.Name()},
				})
				// Applied before the visible one, so it has reached istiod when the visible one works.
				unknown := routeToServiceEntryIn(t, s, gw, hidden.Name(), "hidden", "")
				known := routeToServiceEntryIn(t, s, gw, selected.Name(), "selected", "")
				gw.Expect(t, Request{Host: known}, FromService(ExternalSvc))
				gw.ExpectStable(t, Request{Host: unknown}, notServed())
			},
		},

		// ---- ProxyConfig.connectionSettings timeouts and profile ----
		Feature{
			ID:          "proxyconfig.connection.idle-timeout",
			Description: "connectionSettings.httpIdleTimeout closes idle downstream HTTP/1.1 keep-alive connections after the timeout.",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers:      []string{"ProxyConfig.connectionSettings.httpIdleTimeout"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := proxyConfigGateway(t, s, `{"connectionSettings":{"httpIdleTimeout":"2s"}}`, true)
				// Succeeds only if the proxy closes the connection: otherwise it stays open until the
				// client's read deadline. Envoy closes an idle connection after its drain timeout (5s by
				// default).
				timedGateway(t, gw, rawHTTP(s.Host()+"\r\n\r", "HTTP/1.1 200", 15*time.Second), check.NoError(), 2*time.Second, 10*time.Second)
			},
		},
		Feature{
			ID: "proxyconfig.connection.drain-timeout",
			Description: "connectionSettings.httpDrainTimeout delays closing a draining downstream connection: " +
				"a keep-alive connection reaching its idle timeout is closed only after the drain timeout.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"ProxyConfig.connectionSettings.httpDrainTimeout"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := proxyConfigGateway(t, s, `{"connectionSettings":{"httpIdleTimeout":"1s","httpDrainTimeout":"8s"}}`, true)
				// A proxy closing at the idle timeout, or after a shorter default drain, closes before 8s.
				timedGateway(t, gw, rawHTTP(s.Host()+"\r\n\r", "HTTP/1.1 200", 20*time.Second), check.NoError(), 8*time.Second, 14*time.Second)
			},
		},
		Feature{
			ID: "proxyconfig.connection.max-connection-duration",
			Description: "connectionSettings.httpMaxConnectionDuration closes downstream connections after the duration, " +
				"after completing the request in flight.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"ProxyConfig.connectionSettings.httpMaxConnectionDuration"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := proxyConfigGateway(t, s, `{"connectionSettings":{"httpMaxConnectionDuration":"1s"}}`, true)
				// The request outlives the connection's duration: it completes, then the proxy closes
				// the connection (it would stay open otherwise).
				timedGateway(t, gw, rawHTTP(s.Host()+"\r\n\r", "HTTP/1.1 200", 15*time.Second, "/?delay=2s"),
					check.NoError(), 2*time.Second, 12*time.Second)
			},
		},
		Feature{
			ID:          "proxyconfig.connection.request-headers-timeout",
			Description: "connectionSettings.httpRequestHeadersTimeout answers 408 when the request headers are not complete within the timeout.",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers:      []string{"ProxyConfig.connectionSettings.httpRequestHeadersTimeout"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := proxyConfigGateway(t, s, `{"connectionSettings":{"httpRequestHeadersTimeout":"1s"}}`, true)
				// The client appends "\n": the header block is never terminated by an empty line.
				timedGateway(t, gw, rawHTTP(s.Host()+"\r", "408", 10*time.Second), check.NoError(), time.Second, 8*time.Second)
			},
		},
		Feature{
			ID:          "proxyconfig.connection.stream-idle-timeout",
			Description: "connectionSettings.httpStreamIdleTimeout fails requests whose stream sees no activity for the timeout (408/504).",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers:      []string{"ProxyConfig.connectionSettings.httpStreamIdleTimeout"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := proxyConfigGateway(t, s, `{"connectionSettings":{"httpStreamIdleTimeout":"1s"}}`, true)
				gw.Expect(t, Request{Path: "/?delay=10ms"}, check.OK())
				gw.Expect(t, Request{Path: "/?delay=3s"}, statusIn(408, 504))
			},
		},
		Feature{
			ID:          "proxyconfig.connection.max-stream-duration",
			Description: "connectionSettings.httpMaxStreamDuration fails requests that last longer than the duration (408/504).",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers:      []string{"ProxyConfig.connectionSettings.httpMaxStreamDuration"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := proxyConfigGateway(t, s, `{"connectionSettings":{"httpMaxStreamDuration":"1s"}}`, true)
				gw.Expect(t, Request{Path: "/?delay=10ms"}, check.OK())
				gw.Expect(t, Request{Path: "/?delay=3s"}, statusIn(408, 504))
			},
		},
		Feature{
			ID: "proxyconfig.connection.profile",
			Description: "connectionSettings.profile EDGE applies edge defaults on gateways (headers with underscores rejected, escaped slashes " +
				"redirected, repeated slashes merged) unless a field overrides them; SIDECAR applies none.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers: []string{
				"ProxyConfig.connectionSettings.profile", "ProxyConfig.connectionSettings.profile=EDGE", "ProxyConfig.connectionSettings.profile=SIDECAR",
			},
			Run: func(t framework.TestContext, s *Scenario) {
				hdr := H("x_pcs", "v")
				sidecar := proxyConfigGateway(t, s, `{"connectionSettings":{"profile":"SIDECAR"}}`, true, "sidecar")
				sidecar.Expect(t, Request{Headers: hdr}, check.RequestHeader("x_pcs", "v"))
				sidecar.Expect(t, Request{Path: "/a//b%2Fc"}, Path("/a//b%2Fc"))

				edge := proxyConfigGateway(t, s, `{"connectionSettings":{"profile":"EDGE"}}`, true, "edge")
				edge.Expect(t, Request{Headers: hdr}, check.Status(400))
				edge.Expect(t, Request{Path: "/a%2Fb"}, check.And(statusIn(301, 302, 307, 308), ResponseHeaderContains("Location", "/a/b")))
				edge.Expect(t, Request{Path: "/a//b"}, Path("/a/b"))

				override := proxyConfigGateway(t, s,
					`{"connectionSettings":{"profile":"EDGE","httpHeadersWithUnderscoresAction":"HEADERS_WITH_UNDERSCORES_ALLOW"}}`, true, "override")
				override.Expect(t, Request{Headers: hdr}, check.RequestHeader("x_pcs", "v"))
				override.Expect(t, Request{Path: "/a//b"}, Path("/a/b"))
			},
		},
		Feature{
			ID: "proxyconfig.connection.global-downstream-limit",
			Description: "connectionSettings.globalDownstreamConnectionLimit caps the downstream connections of the whole proxy: " +
				"of concurrent slow requests, those beyond the limit fail.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"ProxyConfig.connectionSettings.globalDownstreamConnectionLimit"},
			Run: func(t framework.TestContext, s *Scenario) {
				downstreamConnectionOverflow(t, proxyConfigGateway(t, s, `{"connectionSettings":{"globalDownstreamConnectionLimit":2}}`, true))
			},
		},
		Feature{
			ID: "proxyconfig.connection.listener-limit",
			Description: "connectionSettings.listenerConnectionLimit caps the downstream connections of each listener: " +
				"of concurrent slow requests to one port, those beyond the limit fail.",
			Roles:      ingressRole,
			RequiresL7: true,
			Covers:     []string{"ProxyConfig.connectionSettings.listenerConnectionLimit"},
			Run: func(t framework.TestContext, s *Scenario) {
				downstreamConnectionOverflow(t, proxyConfigGateway(t, s, `{"connectionSettings":{"listenerConnectionLimit":2}}`, true))
			},
		},
	)
}

// downstreamConnectionOverflow sends 20 concurrent slow requests through gw, whose connection limit is
// 2: the client's sidecar opens a connection per in-flight request, so some requests are served and the
// ones refused at the limit fail with 503. Without the limit all are served.
func downstreamConnectionOverflow(t framework.TestContext, gw *Gateway) {
	t.Helper()
	// The client has no sidecar, so connections over the limit surface as resets/EOF rather than
	// 503s, and the forwarder reports them only as "N/20 requests had errors".
	gw.Expect(t, Request{Path: "/?delay=2s", Count: 20}, func(_ echo.CallResult, err error) error {
		if err == nil {
			return fmt.Errorf("expected some of 20 concurrent connections to be refused, all succeeded")
		}
		var failed, total int
		if _, perr := fmt.Sscanf(partialFailure.FindString(err.Error()), "%d/%d", &failed, &total); perr != nil {
			return fmt.Errorf("expected a partial connection failure, got: %v", err)
		}
		if failed == total {
			return fmt.Errorf("expected some connections under the limit to succeed, got: %v", err)
		}
		return nil
	})
}

var partialFailure = regexp.MustCompile(`\d+/\d+ requests had errors`)

// pathNormalizationGateway creates a gateway routing Host() to the out-of-mesh backend, so the
// backend reports the path as the gateway forwarded it (a backend sidecar would normalize again).
func pathNormalizationGateway(t framework.TestContext, s *Scenario) *Gateway {
	gw := s.NewGateway(t, GatewayOptions{})
	s.Apply(t, externalRoute(gw))
	return gw
}

// routeToServiceEntryIn applies into ns a ServiceEntry for host <id>.{{.Name}}.{{.ExternalHost}},
// resolving to the out-of-mesh backend (extra is appended to its spec, indented 2), and in the
// scenario namespace an HTTPRoute on gw sending <id>.Host() to it. It returns the request host.
func routeToServiceEntryIn(t framework.TestContext, s *Scenario, gw *Gateway, ns, id, extra string) string {
	t.Helper()
	vars := map[string]any{"ID": id, "GW": gw.Name}
	s.ApplyTo(t, ns, `
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: {{.Name}}-{{.ID}}
spec:
  hosts: ["{{.ID}}.{{.Name}}.{{.ExternalHost}}"]
  location: MESH_EXTERNAL
  resolution: DNS
  ports:
  - {number: 80, name: http, protocol: HTTP}
  endpoints:
  - address: {{.External}}.{{.ExternalNamespace}}.svc.cluster.local
`+extra, vars)
	s.Apply(t, `
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: {{.Name}}-{{.ID}}
spec:
  parentRefs: [{name: {{.GW}}}]
  hostnames: ["{{.ID}}.{{.Host}}"]
  rules:
  - backendRefs:
    - {group: networking.istio.io, kind: Hostname, name: "{{.ID}}.{{.Name}}.{{.ExternalHost}}", port: 80}
`, vars)
	return id + "." + s.Host()
}

// tlsOriginationRule is a DestinationRule for the ServiceEntry host of routeToServiceEntryIn(id) that
// originates TLS to the plaintext backend, breaking requests wherever the rule applies.
func tlsOriginationRule(id, extra string) string {
	return `
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.Name}}-` + id + `
spec:
  host: ` + id + `.{{.Name}}.{{.ExternalHost}}` + extra + `
  trafficPolicy:
    tls: {mode: SIMPLE}
`
}

// notServed checks the gateway answered without reaching a backend: a status other than 200, e.g.
// 404 for no route or 5xx for an unknown or unreachable destination.
func notServed() echo.Checker {
	return check.And(check.NoError(), check.NotStatus(200))
}

// rawHTTP is a raw HTTP/1.1 GET (path "/" unless given) over TCP to the gateway's port 80, ending with
// hostTail (the Host header value and what follows it; the echo client appends "\n"). The call
// succeeds only once the response contains expected and the proxy has closed the connection, or the
// timeout expires (an error).
func rawHTTP(hostTail, expected string, timeout time.Duration, path ...string) Request {
	p := "/"
	if len(path) > 0 {
		p = path[0]
	}
	return Request{Scheme: scheme.TCP, Opts: func(o *echo.CallOptions) {
		o.Message = "GET " + p + " HTTP/1.1\r\nHost: " + hostTail
		o.TCP.ExpectedResponse = wrapperspb.String(expected)
		o.Timeout = timeout
	}}
}

// timedGateway sends r to gw until one attempt passes c and takes a duration in [lower, upper).
func timedGateway(t framework.TestContext, gw *Gateway, r Request, c echo.Checker, lower, upper time.Duration) {
	t.Helper()
	var last error
	for attempt := 0; attempt < 5; attempt++ {
		start := time.Now()
		res, err := gw.Call(t, r)
		d := time.Since(start)
		if cerr := c(res, err); cerr != nil {
			last = fmt.Errorf("attempt %d: %v (call error: %v)", attempt, cerr, err)
			continue
		}
		if d < lower || d >= upper {
			last = fmt.Errorf("attempt %d: took %v, expected [%v, %v)", attempt, d, lower, upper)
			continue
		}
		return
	}
	t.Fatalf("%s: %v", r.Path, last)
}
