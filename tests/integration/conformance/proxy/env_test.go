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
	"path"
	"slices"

	"istio.io/api/label"
	"istio.io/istio/pkg/config/protocol"
	"istio.io/istio/pkg/test/echo/common"
	"istio.io/istio/pkg/test/env"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/common/ports"
	"istio.io/istio/pkg/test/framework/components/echo/deployment"
	"istio.io/istio/pkg/test/framework/components/echo/match"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/framework/resource"
	"istio.io/istio/pkg/test/util/file"
)

const (
	// BackendSvc is the in-mesh (sidecar) backend. It has two versions, v1 and v2.
	BackendSvc = "backend"
	// ExternalSvc is an out-of-mesh backend, used for ServiceEntry and TLS origination tests.
	ExternalSvc = "external"
	// ClientSvc is the out-of-mesh client that sends traffic to the gateways.
	ClientSvc = "client"
	// MeshClientSvc is a sidecar-injected client. Its sidecar is the proxy under test for data-plane
	// (outbound) features.
	MeshClientSvc = "client-mesh"
	// AmbientClientSvc is an uninjected client in the ambient namespace: its traffic is handled by
	// ztunnel, and by the destination's waypoint when one is deployed.
	AmbientClientSvc = "client-ambient"
	// ExternalHost is the hostname used to address the external backend through a ServiceEntry.
	ExternalHost = "external.pcs.example.com"
	// BackendTLSHost is the SAN in the echo test server certificate (tests/testdata/certs/dns).
	BackendTLSHost = "server.default.svc"
)

// Env holds everything shared by all features and all proxies: namespaces and echo instances.
// Gateways are not shared across proxies; each feature creates (or reuses) a gateway of the proxy
// under test.
type Env struct {
	// Namespace contains the in-mesh backends, the clients and the gateways.
	Namespace namespace.Instance
	// ExternalNamespace contains the out-of-mesh backend.
	ExternalNamespace namespace.Instance
	// AmbientNamespace contains the ambient client. Its features deploy their own backends here. It
	// is only created when the framework runs in ambient mode (Settings().Ambient), i.e. when
	// ztunnel is installed.
	AmbientNamespace namespace.Instance

	Client   echo.Instance
	Backend  echo.Instances
	External echo.Instances

	// MeshClient is the sidecar-injected client (data-plane role, ModeSidecar).
	MeshClient echo.Instance
	// AmbientClient sends traffic through ztunnel/waypoints.
	AmbientClient echo.Instance

	// SystemNamespace is where Istio, including the ingress and egress gateways, is installed.
	SystemNamespace string
	// EgressGatewayService is the cluster-local hostname of the egress gateway Service, the
	// destination that routes traffic from the mesh into the gateway.
	EgressGatewayService string
	// EgressGatewayLabel is the value of the istio label on egress gateway pods (istio: <label>).
	EgressGatewayLabel string
}

// backendPorts are the ports exposed by the backends.
var backendPorts = echo.Ports{
	ports.HTTP,
	ports.GRPC,
	ports.HTTP2,
	ports.TCP,
	ports.HTTPS,
	ports.TCPServer,
	ports.HTTPWithProxy,
	// Reserved for features that change PeerAuthentication on the backend (workload port 18081).
	ports.AutoHTTP,
	// Reserved for features that apply port-scoped AuthorizationPolicy on the backend.
	{Name: "http-authz", Protocol: protocol.HTTP, ServicePort: 88, WorkloadPort: 18088},
}

func (e *Env) Setup(ctx resource.Context) error {
	var err error
	// Gateways are installed by the suite's Istio installation; their names and labels are
	// configurable, so read them rather than hard-coding the defaults.
	cfg := inst.Settings()
	e.SystemNamespace = cfg.SystemNamespace
	e.EgressGatewayService = fmt.Sprintf("%s.%s.svc.cluster.local", cfg.EgressGatewayServiceName, cfg.EgressGatewayServiceNamespace)
	e.EgressGatewayLabel = cfg.EgressGatewayIstioLabel

	e.Namespace, err = namespace.New(ctx, namespace.Config{Prefix: "pcs", Inject: true})
	if err != nil {
		return err
	}
	e.ExternalNamespace, err = namespace.New(ctx, namespace.Config{Prefix: "pcs-external", Inject: false})
	if err != nil {
		return err
	}

	// The echo test server certificate, baked into the echo image and used for all TLS ports.
	tlsSettings := &common.TLSSettings{
		RootCert:   file.MustAsString(path.Join(env.IstioSrc, "tests/testdata/certs/dns/root-cert.pem")),
		ClientCert: file.MustAsString(path.Join(env.IstioSrc, "tests/testdata/certs/dns/cert-chain.pem")),
		Key:        file.MustAsString(path.Join(env.IstioSrc, "tests/testdata/certs/dns/key.pem")),
		Hostname:   BackendTLSHost,
	}

	echos, err := deployment.New(ctx).
		WithConfig(echo.Config{
			Service:   ClientSvc,
			Namespace: e.Namespace,
			Ports:     echo.Ports{ports.HTTP},
			Subsets: []echo.SubsetConfig{{
				Labels: map[string]string{label.SidecarInject.Name: "false"},
			}},
		}).
		// A sidecar-injected client: for data-plane features the client's sidecar is the proxy under
		// test, so traffic to the backend passes through it.
		WithConfig(echo.Config{
			Service:   MeshClientSvc,
			Namespace: e.Namespace,
			Ports:     echo.Ports{ports.HTTP},
			// A service account named after the service, so its principal is predictable in
			// identity-based policies (.ClientPrincipal).
			ServiceAccount: true,
		}).
		WithConfig(echo.Config{
			Service:   BackendSvc,
			Namespace: e.Namespace,
			Ports:     backendPorts,
			Subsets:   []echo.SubsetConfig{{Version: "v1"}, {Version: "v2"}},
		}).
		WithConfig(echo.Config{
			Service:           ExternalSvc,
			Namespace:         e.ExternalNamespace,
			DefaultHostHeader: ExternalHost,
			Ports:             append(slices.Clone(backendPorts), ports.MTLS),
			TLSSettings:       tlsSettings,
			Subsets: []echo.SubsetConfig{{
				Version: "v1",
				Labels:  map[string]string{label.SidecarInject.Name: "false"},
			}},
		}).
		Build()
	if err != nil {
		return err
	}
	e.Client = match.ServiceName(echo.NamespacedName{Name: ClientSvc, Namespace: e.Namespace}).GetMatches(echos)[0]
	e.Backend = match.ServiceName(echo.NamespacedName{Name: BackendSvc, Namespace: e.Namespace}).GetMatches(echos)
	e.External = match.ServiceName(echo.NamespacedName{Name: ExternalSvc, Namespace: e.ExternalNamespace}).GetMatches(echos)
	e.MeshClient = match.ServiceName(echo.NamespacedName{Name: MeshClientSvc, Namespace: e.Namespace}).GetMatches(echos)[0]

	if ctx.Settings().Ambient {
		if err := e.setupAmbient(ctx); err != nil {
			return err
		}
	}

	// Per-version services, so routes can split traffic between v1 and v2 without Istio subsets.
	return e.createVersionServices(ctx, e.Namespace, BackendSvc)
}

// setupAmbient creates the ambient namespace with an uninjected client. Features deploy their own
// backends there. Traffic is handled by ztunnel, and by a waypoint when one is attached.
func (e *Env) setupAmbient(ctx resource.Context) error {
	var err error
	e.AmbientNamespace, err = namespace.New(ctx, namespace.Config{
		Prefix: "pcs-ambient",
		Inject: false,
		// Ambient capture is opt-in per namespace.
		Labels: map[string]string{"istio.io/dataplane-mode": "ambient"},
	})
	if err != nil {
		return err
	}
	echos, err := deployment.New(ctx).
		WithConfig(echo.Config{
			Service:   AmbientClientSvc,
			Namespace: e.AmbientNamespace,
			Ports:     echo.Ports{ports.HTTP},
			// A service account named after the service, so its principal is predictable in
			// identity-based policies (.ClientPrincipal).
			ServiceAccount: true,
		}).
		Build()
	if err != nil {
		return err
	}
	e.AmbientClient = match.ServiceName(echo.NamespacedName{Name: AmbientClientSvc, Namespace: e.AmbientNamespace}).GetMatches(echos)[0]
	return nil
}

// WorkloadNamespace returns the namespace of the workloads attached to a dataplane mode.
func (e *Env) WorkloadNamespace(mode DataplaneMode) namespace.Instance {
	if mode == ModeSidecar {
		return e.Namespace
	}
	return e.AmbientNamespace
}

// WorkloadClient returns the client whose traffic the proxy under test handles.
func (e *Env) WorkloadClient(mode DataplaneMode) echo.Instance {
	if mode == ModeSidecar {
		return e.MeshClient
	}
	return e.AmbientClient
}

// createVersionServices adds <svc>-v1 / <svc>-v2 services, so features can split traffic by version
// without Istio subsets.
func (e *Env) createVersionServices(ctx resource.Context, ns namespace.Instance, svc string) error {
	return ctx.ConfigKube().Eval(ns.Name(), map[string]any{"svc": svc}, `
{{- range $v := list "v1" "v2" }}
apiVersion: v1
kind: Service
metadata:
  name: {{ $.svc }}-{{ $v }}
spec:
  selector:
    app: {{ $.svc }}
    version: {{ $v }}
  ports:
  - name: http
    port: 80
    targetPort: 18080
  - name: grpc
    port: 7070
    targetPort: 17070
    appProtocol: grpc
  - name: tcp
    port: 9090
    targetPort: 19090
---
{{- end }}
`).Apply()
}
