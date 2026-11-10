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
	"hash/fnv"
	"maps"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s "sigs.k8s.io/gateway-api/apis/v1"

	"istio.io/istio/pkg/config/protocol"
	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/util/retry"
	"istio.io/istio/pkg/test/util/tmpl"
)

// Feature is the unit of conformance: one externally observable behavior.
//
// Features must be written only against Scenario, never against a specific implementation, and
// must only observe behavior black-box: through the echo response, Kubernetes status, or timing.
// Reading proxy config dumps or admin endpoints is not allowed.
type Feature struct {
	// ID is a stable, hierarchical identifier, e.g. "traffic.http.header-match". It is the subtest
	// name, so '-run TestIngressConformance/<proxy>/<id>' selects a single feature.
	ID string
	// Description is a one-sentence statement of the behavior under test.
	Description string
	// Roles are the roles this feature applies to. Required: Register panics without them, and the
	// coverage check fails when they are missing or name an unknown role.
	Roles []Role
	// RequiresL7 marks behavior that is only observable when HTTP/gRPC traffic is processed. Such a
	// feature can only be served by an L7 proxy (gateway, sidecar or waypoint); it is never part of
	// the node (ztunnel) feature set, which is L4 only. The mode-diff suite uses it to classify
	// ambient/sidecar differences.
	RequiresL7 bool
	// Covers lists the API surface this feature exercises, for the field-coverage check
	// (coverage/coverage_test.go). Entries are API field paths ("AuthorizationPolicy.rules.to.operation.paths"),
	// enum values ("AuthorizationPolicy.action=DENY") or annotations ("annotation:<name>"). Entries
	// must be string literals, as the coverage check reads them from source.
	Covers []string
	// NoControl, if set, explains why the feature cannot have a negative control (it has no observable
	// effect to remove, e.g. AUDIT). TestIngressConformanceControls skips it with this reason.
	NoControl string
	// Profile is the conformance profile the feature belongs to. Empty means ProfileCore, except for
	// IDs prefixed "impl-specific.", which default to ProfileImplSpecific (see profile).
	Profile Profile
	// Aliases are former IDs of this feature. Renaming a feature keeps its old ID here, so release
	// note decisions and declared manifests that reference it stay valid. String literals only.
	Aliases []string
	// MeshConfig is a MeshConfig YAML patch applied before each run of the feature and restored after
	// it (top-level fields of the patch replace the installed ones; see istio.Instance.PatchMeshConfig).
	// istiod reloads mesh config without a restart. Mesh config has no direct observable: the feature
	// must assert a positive effect of the patch (Expect retries until it holds) before any negative
	// check. It is config under test, so negative controls run without it.
	MeshConfig string
	// IstiodEnv are istiod environment variables (feature flags) set for the feature. istiod is
	// restarted with them (once per group of features with the same IstiodEnv) and restored
	// afterwards. Like MeshConfig, it is config under test, so negative controls run without it.
	//
	// Features with MeshConfig or IstiodEnv affect the whole mesh: the runner runs them one at a time,
	// after the proxy's parallel features (see meshwide_test.go).
	IstiodEnv map[string]string
	// Run executes the feature. A failing assertion means the proxy does not (fully) support it.
	Run func(t framework.TestContext, s *Scenario)
}

// meshWide reports whether the feature changes mesh-wide state (MeshConfig or istiod), so it cannot
// run in parallel with other features.
func (f Feature) meshWide() bool {
	return f.MeshConfig != "" || len(f.IstiodEnv) > 0
}

// Profile groups features into claims an implementation can make, like Gateway API profiles:
// "agentgateway supports waypoint core" is one checkable statement.
type Profile string

const (
	// ProfileCore is behavior every implementation of the role must have.
	ProfileCore Profile = "core"
	// ProfileExtended is optional behavior; implementations declare whether they support it.
	ProfileExtended Profile = "extended"
	// ProfileImplSpecific is behavior of an implementation-specific API (EnvoyFilter, WasmPlugin),
	// expected to fail on other implementations and never a conformance requirement.
	ProfileImplSpecific Profile = "impl-specific"
)

// profile returns the feature's effective profile.
func (f Feature) profile() Profile {
	switch {
	case f.Profile != "":
		return f.Profile
	case strings.HasPrefix(f.ID, "impl-specific."):
		return ProfileImplSpecific
	default:
		return ProfileCore
	}
}

// security reports whether a silent failure of the feature is a vulnerability.
func (f Feature) security() bool {
	return strings.HasPrefix(f.ID, "security.")
}

var (
	registryMu sync.Mutex
	registry   = map[string]Feature{}
)

// Register adds features to the suite. IDs must be unique.
func Register(fs ...Feature) {
	registryMu.Lock()
	defer registryMu.Unlock()
	for _, f := range fs {
		if _, dup := registry[f.ID]; dup {
			panic("duplicate proxy conformance feature " + f.ID)
		}
		if f.Run == nil || len(f.Roles) == 0 {
			panic("incomplete proxy conformance feature " + f.ID)
		}
		registry[f.ID] = f
	}
}

// featuresFor returns all features applying to the role, sorted by ID.
func featuresFor(r Role) []Feature {
	registryMu.Lock()
	defer registryMu.Unlock()
	var out []Feature
	for _, f := range registry {
		for _, fr := range f.Roles {
			if fr == r {
				out = append(out, f)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ingressRole tags the Gateway API / Istio API features that describe north-south traffic handling.
var ingressRole = []Role{RoleIngress}

// l7Roles tags role-neutral L7 features, written against a Target: the behavior is the same on every
// proxy that processes HTTP, whatever its position (ingress gateway, sidecar, waypoint).
var l7Roles = []Role{RoleIngress, RoleSidecar, RoleWaypoint}

// Scenario is what a feature sees: the proxy under test and helpers that hide all
// implementation-specific details.
type Scenario struct {
	Proxy   Proxy
	Feature Feature
	Env     *Env

	shared *Gateway

	// waypoint is the name of the shared waypoint (RoleWaypoint only). Set by the suite runner.
	waypoint string

	// backend is the destination for dataplane features, deployed on first use (see Backend). It is
	// per feature, so that config and policies are isolated between features running in parallel.
	backend *Backend

	// targetVars are the template variables set by Target (.TargetHost, .Dest, ...).
	targetVars map[string]any

	// control enables negative-control mode (see TestIngressConformanceControls): feature config is
	// stripped from everything the feature applies, and stripped records whether anything was.
	control  bool
	stripped bool

	// Evidence for outcome classification (see classify_test.go).
	evidence evidence
}

// featureConfigKinds are the kinds treated as "config under test" in negative-control mode. Everything
// else (Gateway API routes, Services, ServiceEntries, Istio Gateways, Secrets) is scaffolding.
var featureConfigKinds = map[string]bool{
	"AuthorizationPolicy": true, "RequestAuthentication": true, "PeerAuthentication": true,
	"Telemetry": true, "DestinationRule": true, "VirtualService": true,
	"EnvoyFilter": true, "TrafficExtension": true, "WasmPlugin": true, "ConfigMap": true,
}

var kindRe = regexp.MustCompile(`(?m)^kind:\s*(\S+)`)

// stripFeatureConfig removes featureConfigKinds documents in control mode.
func (s *Scenario) stripFeatureConfig(yaml string) string {
	if !s.control {
		return yaml
	}
	var keep []string
	for _, doc := range regexp.MustCompile(`(?m)^---\s*$`).Split(yaml, -1) {
		if m := kindRe.FindStringSubmatch(doc); m != nil && featureConfigKinds[m[1]] {
			s.stripped = true
			continue
		}
		keep = append(keep, doc)
	}
	return strings.Join(keep, "\n---\n")
}

// Name returns a DNS-label-safe name unique to this (proxy, feature) pair, optionally suffixed.
// It is short enough to leave room for the GatewayClass suffix on generated resources.
func (s *Scenario) Name(suffix ...string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s.Proxy.Name + "/" + s.Feature.ID + "/" + strings.Join(suffix, "/")))
	base := strings.NewReplacer(".", "-", "/", "-", "_", "-").Replace(s.Feature.ID)
	if len(base) > 20 {
		base = base[len(base)-20:]
	}
	base = strings.Trim(base, "-")
	return fmt.Sprintf("%s-%s-%x", s.Proxy.Name[:1], base, h.Sum32()&0xffffff)
}

// Host returns a hostname unique to this (proxy, feature) pair. Use it in route hostnames, so
// features sharing a gateway don't interfere.
func (s *Scenario) Host(prefix ...string) string {
	return strings.Join(append(prefix, s.Name()), ".") + ".example.com"
}

// Namespace returns the namespace holding the config and workloads under test: the gateway
// namespace for gateways, the workload namespace for every other role.
func (s *Scenario) Namespace() string {
	if !s.Proxy.isGateway() {
		return s.Env.WorkloadNamespace(s.Proxy.Mode).Name()
	}
	return s.Env.Namespace.Name()
}

// ServerApp returns the app label of the backend under test. The backend is deployed per feature
// (see Backend), which is what isolates policies between parallel features.
func (s *Scenario) ServerApp() string {
	if s.backend != nil {
		return s.backend.App
	}
	return BackendSvc
}

// serverSvc and serverVersion return the template variables for the backend under test; they are
// empty before the feature deploys its backend.
func (s *Scenario) serverSvc() string {
	if s.backend == nil {
		return ""
	}
	return s.backend.Service
}

func (s *Scenario) serverVersion(v string) string {
	if s.backend == nil {
		return ""
	}
	return s.backend.Service + "-" + v
}

// ClientPrincipal returns the SPIFFE principal of the meshed client under test.
func (s *Scenario) ClientPrincipal() string {
	return "cluster.local/ns/" + s.clientNamespace() + "/sa/" + s.clientServiceAccount()
}

// clientNamespace and clientServiceAccount identify the meshed client under test.
func (s *Scenario) clientNamespace() string {
	if s.Proxy.Mode == ModeAmbient {
		return s.Env.AmbientNamespace.Name()
	}
	return s.Env.Namespace.Name()
}

func (s *Scenario) clientServiceAccount() string {
	if s.Proxy.Mode == ModeAmbient {
		return AmbientClientSvc
	}
	return MeshClientSvc
}

// Vars returns the default template variables for config. Features can add more.
//
//	.Namespace         namespace of gateways, backends and config
//	.ExternalNamespace namespace of the out-of-mesh backend
//	.SystemNamespace   namespace of the Istio installation (istiod, ingress/egress gateways)
//	.GatewayClass      GatewayClass of the proxy under test
//	.Host              hostname unique to this feature (see Host)
//	.Backend           in-mesh backend service name (v1+v2), and .BackendV1 / .BackendV2
//	.External          out-of-mesh backend service name
//	.ExternalHost      hostname of the external ServiceEntry
//	.EgressService     cluster-local hostname of the egress gateway Service
//	.EgressLabel       value of the istio label on egress gateway pods
//	.Name              name unique to this feature (see Name)
//	.ClientPrincipal   SPIFFE principal of the meshed client (workload roles), and .ClientNamespace / .ClientSA
//
// After Scenario.Target, the Target variables (.TargetHost, .Dest, .PolicyAttachment, ...) are set too.
func (s *Scenario) Vars(extra ...map[string]any) map[string]any {
	v := map[string]any{
		"Namespace":         s.Namespace(),
		"ExternalNamespace": s.Env.ExternalNamespace.Name(),
		"SystemNamespace":   s.Env.SystemNamespace,
		"GatewayClass":      s.Proxy.GatewayClass,
		"Host":              s.Host(),
		"Backend":           BackendSvc,
		"BackendV1":         BackendSvc + "-v1",
		"BackendV2":         BackendSvc + "-v2",
		"External":          ExternalSvc,
		"ExternalHost":      ExternalHost,
		"EgressService":     s.Env.EgressGatewayService,
		"EgressLabel":       s.Env.EgressGatewayLabel,
		"Name":              s.Name(),
		// Dataplane role: the destination (see Backend) and the tested client's identity.
		"ServerApp":       s.ServerApp(),
		"ServerSvc":       s.serverSvc(),
		"ServerV1":        s.serverVersion("v1"),
		"ServerV2":        s.serverVersion("v2"),
		"ClientPrincipal": s.ClientPrincipal(),
		"ClientNamespace": s.clientNamespace(),
		"ClientSA":        s.clientServiceAccount(),
	}
	maps.Copy(v, s.targetVars)
	for _, e := range extra {
		maps.Copy(v, e)
	}
	return v
}

// Apply applies templated config into the scenario namespace. It is removed when the feature ends.
func (s *Scenario) Apply(t framework.TestContext, yaml string, extra ...map[string]any) {
	t.Helper()
	s.ApplyTo(t, s.Namespace(), yaml, extra...)
}

// ApplyTo applies templated config into another namespace.
func (s *Scenario) ApplyTo(t framework.TestContext, ns, yaml string, extra ...map[string]any) {
	t.Helper()
	out := s.stripFeatureConfig(tmpl.MustEvaluate(yaml, s.Vars(extra...)))
	if strings.TrimSpace(strings.ReplaceAll(out, "---", "")) == "" {
		return
	}
	s.evidence.recordApplied(ns, out)
	if err := t.ConfigIstio().YAML(ns, out).Apply(); err != nil {
		s.evidence.recordApplyError(err)
		t.Fatalf("applying config: %v", err)
	}
}

// NoConnectionReuse makes the client open a new connection per request to host. Proxies apply HTTP
// filter changes (new authorization rules, for example) only to new connections, so a feature that
// changes authorization after earlier traffic to the same destination must not reuse connections or
// it can keep observing the old rules. In ambient, ztunnel pools HBONE connections, so this is
// expressed as a DestinationRule (the client's connection pool) rather than a client option. Call it
// before the feature's first request to host.
func (s *Scenario) NoConnectionReuse(t framework.TestContext, host string) {
	t.Helper()
	s.Apply(t, `
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: {{.NoConnectionReuseDR}}
spec:
  host: {{.NoConnectionReuseHost}}
  trafficPolicy:
    connectionPool:
      http:
        maxRequestsPerConnection: 1
`, map[string]any{"NoConnectionReuseDR": s.Name("noreuse"), "NoConnectionReuseHost": host})
}

// Gateway is a deployed gateway of the proxy under test.
type Gateway struct {
	Name      string
	Namespace string
	// Address is the address traffic is sent to (from Gateway status).
	Address string
	s       *Scenario
}

// DefaultGatewayListeners is a single HTTP listener on port 80 accepting routes from any namespace.
const DefaultGatewayListeners = `
  - name: http
    port: 80
    protocol: HTTP
    allowedRoutes:
      namespaces:
        from: All
`

// SharedGateway returns a gateway with DefaultGatewayListeners that is shared by all features of
// the current proxy. Use it for route-level features with a unique Host(); use NewGateway for
// anything that changes gateway-wide behavior (policies, listeners, annotations).
func (s *Scenario) SharedGateway() *Gateway {
	// Bind a copy to this scenario, so requests default to this feature's Host().
	g := *s.shared
	g.s = s
	return &g
}

// GatewayOptions customizes NewGateway.
type GatewayOptions struct {
	// Listeners is the YAML list of listeners (indented for spec.listeners). Defaults to
	// DefaultGatewayListeners. It is templated with Scenario.Vars.
	Listeners string
	// Annotations and Labels are set on the Gateway object.
	Annotations map[string]string
	Labels      map[string]string
	// Infrastructure is the YAML body of spec.infrastructure (indented for spec.infrastructure).
	Infrastructure string
	// Addresses is the YAML body of spec.addresses.
	Addresses string
	// ListenerVars are extra template variables for Listeners.
	ListenerVars map[string]any
	// TLS is the YAML body of spec.tls (indented for spec.tls), e.g. frontend validation.
	TLS string
	// Suffix distinguishes multiple gateways in one feature.
	Suffix string
	// SkipWait returns without waiting for the Gateway to be Programmed.
	SkipWait bool
}

// NewGateway deploys a dedicated gateway of the proxy under test, and waits until it is
// programmed. It is removed when the feature ends.
func (s *Scenario) NewGateway(t framework.TestContext, o GatewayOptions) *Gateway {
	t.Helper()
	if o.Listeners == "" {
		o.Listeners = DefaultGatewayListeners
	}
	if s.control && (len(o.Annotations) > 0 || len(o.Labels) > 0 || o.Infrastructure != "" || o.Addresses != "" || o.TLS != "") {
		// Gateway-level settings are the config under test for infra/ProxyConfig/TLS features.
		o.Annotations, o.Labels, o.Infrastructure, o.Addresses, o.TLS = nil, nil, "", "", ""
		s.stripped = true
	}
	name := s.Name("gw", o.Suffix)
	s.Apply(t, `
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: {{.GatewayName}}
{{- with .GatewayLabels }}
  labels:
{{- range $k, $v := . }}
    {{ $k }}: {{ $v | quote }}
{{- end }}
{{- end }}
{{- with .GatewayAnnotations }}
  annotations:
{{- range $k, $v := . }}
    {{ $k }}: {{ $v | quote }}
{{- end }}
{{- end }}
spec:
  gatewayClassName: {{.GatewayClass}}
{{- with .Infrastructure }}
  infrastructure:
{{ . }}
{{- end }}
{{- with .Addresses }}
  addresses:
{{ . }}
{{- end }}
{{- with .GatewayTLS }}
  tls:
{{ . }}
{{- end }}
  listeners:
`+tmpl.MustEvaluate(o.Listeners, s.Vars(o.ListenerVars)), map[string]any{
		"GatewayName":        name,
		"GatewayLabels":      o.Labels,
		"GatewayAnnotations": o.Annotations,
		"Infrastructure":     o.Infrastructure,
		"Addresses":          o.Addresses,
		"GatewayTLS":         o.TLS,
	})
	gw := &Gateway{Name: name, Namespace: s.Namespace(), s: s}
	if !o.SkipWait {
		gw.WaitProgrammed(t)
	}
	return gw
}

// Get returns the current Gateway object.
func (g *Gateway) Get(t framework.TestContext) (*k8s.Gateway, error) {
	return t.Clusters().Default().GatewayAPI().GatewayV1().Gateways(g.Namespace).
		Get(context.Background(), g.Name, metav1.GetOptions{})
}

// WaitProgrammed waits for the Gateway to report Programmed=True and an address.
func (g *Gateway) WaitProgrammed(t framework.TestContext) {
	t.Helper()
	retry.UntilSuccessOrFail(t, func() error {
		obj, err := g.Get(t)
		if err != nil {
			return err
		}
		if !conditionTrue(obj.Status.Conditions, string(k8s.GatewayConditionProgrammed)) {
			return fmt.Errorf("gateway %s not programmed: %+v", g.Name, obj.Status.Conditions)
		}
		if len(obj.Status.Addresses) == 0 {
			return fmt.Errorf("gateway %s has no address", g.Name)
		}
		g.Address = obj.Status.Addresses[0].Value
		return nil
	}, retry.Timeout(3*time.Minute), retry.Delay(time.Second))
	// Send traffic to the Service's ClusterIP rather than the status address: LoadBalancer IPs
	// (MetalLB in kind) are recycled quickly when many gateways come and go, and their L2
	// announcements lag, which shows up as "no route to host" unrelated to the proxy. The Service is
	// found via the standard gateway-name label, so this stays implementation-neutral. Gateways
	// with a Hostname address (manual deployment) have no own Service; keep the status address.
	if svc, err := g.Service(t); err == nil && svc.Spec.ClusterIP != "" && svc.Spec.ClusterIP != "None" {
		g.Address = svc.Spec.ClusterIP
	}
	// Programmed does not imply the proxy pods are ready (e.g. first image pull); wait, so that
	// features measure behavior rather than startup latency. Gateways without their own Service
	// (manual deployment via a Hostname address) have no pods of their own.
	if _, err := g.Service(t); err != nil {
		return
	}
	retry.UntilSuccessOrFail(t, func() error {
		pods := g.Pods(t)
		if len(pods) == 0 {
			return fmt.Errorf("no pods for gateway %s", g.Name)
		}
		for _, p := range pods {
			if !podReady(p) {
				return fmt.Errorf("gateway pod %s not ready (%s)", p.Name, podStatus(p))
			}
		}
		return nil
	}, retry.Timeout(5*time.Minute), retry.Delay(time.Second))
}

// Selector returns the label selector matching this gateway's pods, implementation-neutral.
func (g *Gateway) Selector() string {
	return "gateway.networking.k8s.io/gateway-name=" + g.Name
}

func conditionTrue(conds []metav1.Condition, typ string) bool {
	for _, c := range conds {
		if c.Type == typ {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

// Request describes a call through a gateway. Zero values give an HTTP/1.1 GET of "/" to port 80
// with the scenario Host().
type Request struct {
	Host    string
	Port    int
	Path    string
	Method  string
	Headers http.Header
	Scheme  scheme.Instance
	HTTP2   bool
	// Count of requests to send (default 1).
	Count int
	// Address overrides the gateway address (e.g. a pod IP, for ports not exposed on the Service).
	Address string
	// Opts allows overriding anything else; it is applied last.
	Opts func(*echo.CallOptions)
}

func (g *Gateway) options(r Request, check echo.Checker) echo.CallOptions {
	if r.Host == "" {
		r.Host = g.s.Host()
	}
	if r.Port == 0 {
		r.Port = 80
	}
	if r.Scheme == "" {
		r.Scheme = scheme.HTTP
	}
	headers := r.Headers.Clone()
	if headers == nil {
		headers = http.Header{}
	}
	headers.Set("Host", r.Host)
	addr := g.Address
	if r.Address != "" {
		addr = r.Address
	}
	o := echo.CallOptions{
		Address: addr,
		Port:    echo.Port{ServicePort: r.Port, Protocol: protocolFor(r.Scheme)},
		Scheme:  r.Scheme,
		Count:   r.Count,
		HTTP: echo.HTTP{
			Path:    r.Path,
			Method:  r.Method,
			Headers: headers,
			HTTP2:   r.HTTP2,
		},
		Check: check,
	}
	if r.Opts != nil {
		r.Opts(&o)
	}
	return o
}

func protocolFor(s scheme.Instance) protocol.Instance {
	switch s {
	case scheme.GRPC:
		return protocol.GRPC
	case scheme.TCP:
		return protocol.TCP
	case scheme.HTTPS:
		return protocol.HTTPS
	case scheme.TLS:
		return protocol.TLS
	default:
		return protocol.HTTP
	}
}

// Expect sends the request from the client through the gateway, retrying until the check passes
// (config propagation is asynchronous) or the call timeout expires.
func (g *Gateway) Expect(t framework.TestContext, r Request, check echo.Checker) echo.CallResult {
	t.Helper()
	return g.s.Env.Client.CallOrFail(t, g.options(r, g.s.observe(check)))
}

// ExpectStable is like Expect but requires the check to pass consistently for a while. Use it for
// negative checks ("header is absent") whose baseline is the opposite (the header is present without
// the config): the check then fails until the config propagates, and stability guards against a
// partial rollout. When the baseline already satisfies the check, stability proves nothing: observe
// a positive witness first (see witness_test.go). The negative controls verify the baseline.
func (g *Gateway) ExpectStable(t framework.TestContext, r Request, check echo.Checker) echo.CallResult {
	t.Helper()
	o := g.options(r, g.s.observe(check))
	o.Retry.Options = []retry.Option{retry.Timeout(time.Minute), retry.Converge(10), retry.Delay(200 * time.Millisecond)}
	return g.s.Env.Client.CallOrFail(t, o)
}

// Call sends a single request without retrying or checking; used for timing-based features.
func (g *Gateway) Call(t framework.TestContext, r Request) (echo.CallResult, error) {
	t.Helper()
	o := g.options(r, nil)
	o.Retry.NoRetry = true
	return g.s.Env.Client.Call(o)
}

// sharedVars returns template vars referencing the shared gateway.
func (s *Scenario) sharedVars() map[string]any {
	return map[string]any{"SharedGateway": s.shared.Name}
}
