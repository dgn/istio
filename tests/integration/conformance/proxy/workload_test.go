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
	"net/http"
	"time"

	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/common/ports"
	"istio.io/istio/pkg/test/framework/components/echo/deployment"
	"istio.io/istio/pkg/test/util/retry"
)

// Workload is the data-plane target of a feature: requests sent by a meshed client, handled by the
// proxy under test, to a meshed server.
type Workload struct {
	s      *Scenario
	client echo.Instance
	server string
}

// Workload returns the data-plane target for this scenario, deploying the feature's backend if
// needed. Call it before applying config: it sets the template variables the config uses.
func (s *Scenario) Workload(t framework.TestContext) *Workload {
	return &Workload{s: s, client: s.Env.WorkloadClient(s.Proxy.Mode), server: s.Backend(t).Service}
}

// External returns a target for traffic leaving the mesh: a sidecar client addresses the external
// host, and the proxy under test (an egress gateway) routes it out. It deploys no backend, so the
// feature must create the ServiceEntry itself.
func (s *Scenario) External(host string) *Workload {
	return &Workload{s: s, client: s.Env.WorkloadClient(ModeSidecar), server: host}
}

// Client returns the client whose traffic the proxy under test handles.
func (w *Workload) Client() echo.Instance { return w.client }

// Backend is the destination under test. It is deployed per feature (v1 and v2) so that config and
// policies cannot leak between features running in parallel.
type Backend struct {
	// Service is the host the client dials and features configure. It selects only the v1 pods, so a
	// feature that does not select v2 cannot pass by chance: negative controls depend on it.
	Service string
	// App is the app label of the backend deployments, selecting both versions. Policies use it.
	App string
	// V1 and V2 select a single version, so features can route by version without DestinationRule.
	V1, V2 string
	// Instances are the echo instances of both versions (e.g. to read their logs).
	Instances echo.Instances
}

// Backend returns the feature's backend, deploying it on first use.
func (s *Scenario) Backend(t framework.TestContext) *Backend {
	t.Helper()
	if s.backend != nil {
		return s.backend
	}
	ns := s.Env.WorkloadNamespace(s.Proxy.Mode)
	name := s.Name("be")
	app := name + "-app"
	instances, err := deployment.New(t).WithConfig(echo.Config{
		Service:   app,
		Namespace: ns,
		Ports:     echo.Ports{ports.HTTP},
		Subsets:   []echo.SubsetConfig{{Version: "v1"}, {Version: "v2"}},
	}).Build()
	if err != nil {
		t.Fatalf("failed deploying backend %s: %v", name, err)
	}
	s.backend = &Backend{Service: name, App: app, V1: name + "-v1", V2: name + "-v2", Instances: instances}

	// Our own services, rather than the ones echo creates: the host the feature configures serves v1
	// only, so v2 is reachable only through config under test. For ambient L7 features the service
	// carries the waypoint attachment.
	if s.needsWaypoint() && s.waypoint == "" {
		t.Fatalf("feature %s needs a waypoint, but the suite deployed none for proxy %s", s.Feature.ID, s.Proxy.Name)
	}
	err = t.ConfigKube().Eval(ns.Name(), map[string]any{"svc": name, "app": app, "waypoint": s.waypoint}, `
apiVersion: v1
kind: Service
metadata:
  name: {{ .svc }}
{{- with .waypoint }}
  labels:
    istio.io/use-waypoint: {{ . }}
{{- end }}
spec:
  selector:
    app: {{ .app }}
    version: v1
  ports:
  - name: http
    port: 80
    targetPort: 18080
---
apiVersion: v1
kind: Service
metadata:
  name: {{ .svc }}-v1
spec:
  selector:
    app: {{ .app }}
    version: v1
  ports:
  - name: http
    port: 80
    targetPort: 18080
---
apiVersion: v1
kind: Service
metadata:
  name: {{ .svc }}-v2
spec:
  selector:
    app: {{ .app }}
    version: v2
  ports:
  - name: http
    port: 80
    targetPort: 18080
`).Apply()
	if err != nil {
		t.Fatalf("failed creating services for backend %s: %v", name, err)
	}
	return s.backend
}

// needsWaypoint reports whether this feature's traffic is processed by a waypoint.
func (s *Scenario) needsWaypoint() bool {
	return s.Proxy.needsWaypoint()
}

// DeployWaypoint deploys the waypoint shared by this proxy's features and waits until it is ready.
// Features attach their backend Service to it with istio.io/use-waypoint.
func (s *Scenario) DeployWaypoint(t framework.TestContext) string {
	t.Helper()
	name := s.Name("wp")
	s.Apply(t, `
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: {{.WaypointName}}
  labels:
    istio.io/waypoint-for: service
spec:
  gatewayClassName: {{.GatewayClass}}
  listeners:
  - name: mesh
    port: 15008
    protocol: HBONE
`, map[string]any{"WaypointName": name})
	wp := &Gateway{Name: name, Namespace: s.Namespace(), s: s}
	retry.UntilSuccessOrFail(t, func() error {
		pods := wp.Pods(t)
		if len(pods) == 0 {
			return fmt.Errorf("no pods for waypoint %s", name)
		}
		for _, p := range pods {
			if !podReady(p) {
				return fmt.Errorf("waypoint pod %s not ready (%s)", p.Name, podStatus(p))
			}
		}
		return nil
	}, retry.Timeout(5*time.Minute), retry.Delay(time.Second))
	return name
}

// options mirrors Gateway.options: the server is dialed by name (in-mesh DNS), so traffic is
// intercepted by the proxy under test.
func (w *Workload) options(r Request, check echo.Checker) echo.CallOptions {
	if r.Address == "" {
		r.Address = w.server
	}
	if r.Host == "" {
		r.Host = w.server
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
	o := echo.CallOptions{
		Address: r.Address,
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

// Expect sends the request from the meshed client through the proxy under test, retrying until the
// check passes (config propagation is asynchronous) or the call timeout expires.
func (w *Workload) Expect(t framework.TestContext, r Request, check echo.Checker) echo.CallResult {
	t.Helper()
	return w.client.CallOrFail(t, w.options(r, w.s.observe(check)))
}

// ExpectStable is like Expect but requires the check to pass consistently for a while. Use it for
// negative checks that would otherwise pass trivially before config is applied.
func (w *Workload) ExpectStable(t framework.TestContext, r Request, check echo.Checker) echo.CallResult {
	t.Helper()
	o := w.options(r, w.s.observe(check))
	o.Retry.Options = []retry.Option{retry.Timeout(time.Minute), retry.Converge(10), retry.Delay(200 * time.Millisecond)}
	return w.client.CallOrFail(t, o)
}

// Call sends a single request without retrying or checking.
func (w *Workload) Call(t framework.TestContext, r Request) (echo.CallResult, error) {
	t.Helper()
	o := w.options(r, nil)
	o.Retry.NoRetry = true
	return w.client.Call(o)
}
