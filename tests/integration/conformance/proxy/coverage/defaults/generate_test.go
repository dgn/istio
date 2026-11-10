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

package defaults

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"istio.io/api/label"
	"istio.io/istio/pilot/pkg/features"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pilot/test/xds"
	"istio.io/istio/pkg/config/constants"
	"istio.io/istio/pkg/config/mesh"
	"istio.io/istio/pkg/test"
)

// The scenario is deliberately minimal: every path in the inventory should be something istiod
// emits for (almost) any mesh, not something a specific user config asked for. One Service with
// one workload is the smallest input that makes istiod generate inbound and outbound config; the
// ingress Gateway and VirtualService are the smallest input for a router to have a route at all.
const (
	// echoService is a plain HTTP Service with one ready workload. With ambient, the workload is
	// captured by ztunnel and the Service uses the waypoint.
	echoService = `apiVersion: v1
kind: Service
metadata:
  name: echo
  namespace: default
  labels:
    app: echo
{{AMBIENT_SVC_LABELS}}
spec:
  clusterIP: 10.0.0.10
  selector:
    app: echo
  ports:
  - name: http
    port: 80
    targetPort: 8080
---
apiVersion: v1
kind: Pod
metadata:
  name: echo-a
  namespace: default
  labels:
    app: echo
{{AMBIENT_POD_ANNOTATIONS}}
spec: {}
status:
  conditions:
  - status: "True"
    type: Ready
  podIP: 10.1.0.1
  podIPs:
  - ip: 10.1.0.1
`

	// ingress is an Istio ingress Gateway with one HTTP server and a VirtualService bound to it.
	ingress = `apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: ingress
  namespace: istio-system
spec:
  selector:
    istio: ingressgateway
  servers:
  - port:
      number: 80
      name: http
      protocol: HTTP
    hosts:
    - "*"
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: echo
  namespace: default
spec:
  hosts:
  - echo.example.com
  gateways:
  - istio-system/ingress
  http:
  - route:
    - destination:
        host: echo.default.svc.cluster.local
        port:
          number: 80
`

	// waypoint is a namespace waypoint, built the way pilot/pkg/xds/waypoint_test.go builds one:
	// the Gateway, its Service, and a WorkloadEntry whose address is the waypoint proxy's IP.
	waypoint = `apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: waypoint
  namespace: default
spec:
  gatewayClassName: istio-waypoint
  listeners:
  - name: mesh
    port: 15008
    protocol: HBONE
status:
  addresses:
  - type: Hostname
    value: waypoint.default.svc.cluster.local
---
apiVersion: v1
kind: Service
metadata:
  labels:
    gateway.istio.io/managed: istio.io-mesh-controller
    gateway.networking.k8s.io/gateway-name: waypoint
    istio.io/gateway-name: waypoint
  name: waypoint
  namespace: default
spec:
  clusterIP: 10.0.0.20
  ports:
  - appProtocol: hbone
    name: mesh
    port: 15008
  selector:
    gateway.networking.k8s.io/gateway-name: waypoint
---
apiVersion: networking.istio.io/v1
kind: WorkloadEntry
metadata:
  name: waypoint-a
  namespace: default
spec:
  address: 10.1.0.3
  labels:
    gateway.networking.k8s.io/gateway-name: waypoint
`
)

// proxyVersion is the Istio version the generated proxies claim. SetupProxy defaults to an old
// version, which would select compatibility code paths; the inventory must reflect what istiod
// sends a current proxy.
const proxyVersion = "1.99.0"

// profile is the istiod configuration a scenario runs with.
type profile int

const (
	// profileDefault is istiod as installed by the default profile: ambient disabled.
	profileDefault profile = iota
	// profileAmbient is istiod with PILOT_ENABLE_AMBIENT=true. The ambient-gated flags are
	// registered at init with their non-ambient defaults, so the test sets the ones whose
	// ambient default differs (pilot/pkg/features/ambient.go registerAmbient) by hand.
	profileAmbient
)

func (p profile) String() string {
	if p == profileAmbient {
		return "ambient"
	}
	return "default"
}

// generated is the xDS istiod produced for one proxy, by resource kind ("listener", "cluster",
// "route", "workload", "service").
type generated map[string][]proto.Message

// newServer starts a fake istiod with the scenario config for the given profile.
func newServer(t *testing.T, p profile) *xds.FakeDiscoveryServer {
	t.Helper()
	svc := echoService
	cfg := []string{}
	if p == profileAmbient {
		for _, f := range []*bool{
			&features.EnableAmbient,
			&features.EnableAmbientWaypoints,
			&features.EnableHBONESend,
			&features.EnableSidecarHBONEListening,
			&features.EnableIngressWaypointRouting,
			&features.EnableAmbientWaypointMultiNetwork,
		} {
			test.SetForTest(t, f, true)
		}
		svc = strings.ReplaceAll(svc, "{{AMBIENT_SVC_LABELS}}", "    istio.io/use-waypoint: waypoint")
		svc = strings.ReplaceAll(svc, "{{AMBIENT_POD_ANNOTATIONS}}", "  annotations:\n    ambient.istio.io/redirection: enabled")
		cfg = append(cfg, svc, waypoint)
	} else {
		svc = strings.ReplaceAll(svc, "{{AMBIENT_SVC_LABELS}}\n", "")
		svc = strings.ReplaceAll(svc, "{{AMBIENT_POD_ANNOTATIONS}}\n", "")
		cfg = append(cfg, svc, ingress)
	}
	c := strings.Join(cfg, "\n---\n")
	// mesh.DefaultMeshConfig is istiod's compiled-in default; the istiod Helm chart (the product
	// default install) additionally enables the Prometheus metrics provider, which adds the stats
	// filters every proxy gets.
	mc := mesh.DefaultMeshConfig()
	mc.DefaultProviders.Metrics = []string{"prometheus"}
	// The ambient index and the kube registry read Istio objects from the (fake) API server and the
	// config store reads them from ConfigString, so every object goes to both, like waypoint_test.go.
	return xds.NewFakeDiscoveryServer(t, xds.FakeOptions{
		ConfigString:           c,
		KubernetesObjectString: c,
		MeshConfig:             mc,
	})
}

// envoyXDS returns LDS, CDS and RDS for the proxy.
func envoyXDS(d *xds.FakeDiscoveryServer, p *model.Proxy) generated {
	out := generated{}
	ls := d.Listeners(p)
	for _, l := range ls {
		out["listener"] = append(out["listener"], l)
	}
	for _, c := range d.Clusters(p) {
		out["cluster"] = append(out["cluster"], c)
	}
	for _, r := range d.RoutesFromListeners(p, ls) {
		out["route"] = append(out["route"], r)
	}
	return out
}

// proxyCase is one proxy type in the inventory.
type proxyCase struct {
	// name is the first path segment of the proxy's inventory entries.
	name string
	// roles are the PCS roles (catalog.yaml) whose features test this proxy type. A feature
	// decision for a path of this proxy must name a feature with one of these roles: an
	// ingress-only feature does not show that a sidecar implementation replicates the default.
	roles   []string
	profile profile
	// generate returns the proxy's xDS from a server running profile.
	generate func(t *testing.T, d *xds.FakeDiscoveryServer) generated
}

var proxyCases = []proxyCase{
	{
		name:    "sidecar",
		roles:   []string{"sidecar"},
		profile: profileDefault,
		generate: func(t *testing.T, d *xds.FakeDiscoveryServer) generated {
			lbls := map[string]string{"app": "echo"}
			return envoyXDS(d, d.SetupProxy(&model.Proxy{
				Type:            model.SidecarProxy,
				ID:              "echo-a.default",
				ConfigNamespace: "default",
				IPAddresses:     []string{"10.1.0.1"},
				Labels:          lbls,
				Metadata:        &model.NodeMetadata{IstioVersion: proxyVersion, Labels: lbls},
			}))
		},
	},
	{
		name:    "router",
		roles:   []string{"ingress", "egress"},
		profile: profileDefault,
		generate: func(t *testing.T, d *xds.FakeDiscoveryServer) generated {
			lbls := map[string]string{"istio": "ingressgateway"}
			return envoyXDS(d, d.SetupProxy(&model.Proxy{
				Type:            model.Router,
				ID:              "istio-ingressgateway-a.istio-system",
				ConfigNamespace: "istio-system",
				IPAddresses:     []string{"10.1.0.2"},
				Labels:          lbls,
				Metadata:        &model.NodeMetadata{IstioVersion: proxyVersion, Labels: lbls},
			}))
		},
	},
	{
		name:    "waypoint",
		roles:   []string{"waypoint"},
		profile: profileAmbient,
		generate: func(t *testing.T, d *xds.FakeDiscoveryServer) generated {
			lbls := map[string]string{
				label.IoK8sNetworkingGatewayGatewayName.Name: "waypoint",
				label.GatewayManaged.Name:                    constants.ManagedGatewayMeshControllerLabel,
			}
			return envoyXDS(d, d.SetupProxy(&model.Proxy{
				Type:            model.Waypoint,
				ID:              "waypoint-a.default",
				ConfigNamespace: "default",
				IPAddresses:     []string{"10.1.0.3"},
				Labels:          lbls,
				Metadata:        &model.NodeMetadata{IstioVersion: proxyVersion, Labels: lbls},
			}))
		},
	},
	{
		// ztunnel gets the workload API (WDS), not LDS/CDS/RDS. Its authorization resources are
		// only generated for user policies, so they are not defaults.
		name:    "ztunnel",
		roles:   []string{"node"},
		profile: profileAmbient,
		generate: func(t *testing.T, d *xds.FakeDiscoveryServer) generated {
			out := generated{}
			addrs, _ := d.Discovery.Env.AmbientIndexes.AddressInformation(nil)
			for _, a := range addrs {
				if w := a.GetWorkload(); w != nil {
					out["workload"] = append(out["workload"], w)
				}
				if s := a.GetService(); s != nil {
					out["service"] = append(out["service"], s)
				}
			}
			if len(out["workload"]) == 0 || len(out["service"]) == 0 {
				t.Fatalf("ambient index produced %d workloads and %d services, want both", len(out["workload"]), len(out["service"]))
			}
			return out
		},
	},
}
