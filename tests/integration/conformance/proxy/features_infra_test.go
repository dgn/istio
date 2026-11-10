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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"istio.io/istio/pkg/ptr"
	"istio.io/istio/pkg/test/framework"
)

// Gateway infrastructure provisioning (the "deployment controller") and Gateway/route status.
// These are control-plane behaviors, but they are per-implementation (each GatewayClass has its own
// controller and templates), so they belong in the matrix.
func init() {
	Register(
		Feature{
			ID:          "infra.service-type-annotation",
			Description: "The networking.istio.io/service-type annotation sets the generated Service type.",
			Roles:       ingressRole,
			Covers:      []string{"annotation:networking.istio.io/service-type"},
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{Annotations: map[string]string{"networking.istio.io/service-type": "ClusterIP"}})
				EventuallyKube(t, func() error {
					svc, err := gw.Service(t)
					if err != nil {
						return err
					}
					if svc.Spec.Type != corev1.ServiceTypeClusterIP {
						return fmt.Errorf("service type %s, want ClusterIP", svc.Spec.Type)
					}
					return nil
				})
				s.Apply(t, httpRouteTo(gw, "{{.BackendV1}}", 80))
				gw.Expect(t, Request{}, Version("v1"))
			},
		},
		Feature{
			ID:          "infra.gateway-metadata-copied",
			Description: "Without spec.infrastructure, the Gateway's own labels and annotations are copied to generated resources.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{
					Labels:      map[string]string{"pcs.istio.io/label": "lv"},
					Annotations: map[string]string{"pcs.istio.io/annotation": "av"},
				})
				EventuallyKube(t, func() error {
					svc, err := gw.Service(t)
					if err != nil {
						return err
					}
					if svc.Labels["pcs.istio.io/label"] != "lv" || svc.Annotations["pcs.istio.io/annotation"] != "av" {
						return fmt.Errorf("service missing copied metadata: %v %v", svc.Labels, svc.Annotations)
					}
					return nil
				})
			},
		},
		Feature{
			ID:          "infra.parametersref-configmap",
			Description: "spec.infrastructure.parametersRef to a ConfigMap overlays the generated Deployment and Service.",
			Roles:       ingressRole,
			Covers:      []string{"gateway-api-ext:parametersref-overlay:deployment", "gateway-api-ext:parametersref-overlay:service"},
			Run: func(t framework.TestContext, s *Scenario) {
				s.Apply(t, `
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{.Name}}-params
data:
  deployment: |
    spec:
      replicas: 2
  service: |
    metadata:
      annotations:
        pcs.istio.io/from-params: "yes"
`)
				gw := s.NewGateway(t, GatewayOptions{Infrastructure: `
    parametersRef:
      group: ""
      kind: ConfigMap
      name: ` + s.Name() + `-params
`})
				EventuallyKube(t, func() error {
					d, err := gw.Deployment(t)
					if err != nil {
						return err
					}
					if ptr.OrEmpty(d.Spec.Replicas) != 2 {
						return fmt.Errorf("replicas %d, want 2", ptr.OrEmpty(d.Spec.Replicas))
					}
					svc, err := gw.Service(t)
					if err != nil {
						return err
					}
					if svc.Annotations["pcs.istio.io/from-params"] != "yes" {
						return fmt.Errorf("service overlay not applied: %v", svc.Annotations)
					}
					return nil
				})
			},
		},
		Feature{
			ID:          "infra.name-override-annotation",
			Description: "The gateway.istio.io/name-override annotation sets the name of the generated Deployment and Service.",
			Roles:       ingressRole,
			Covers:      []string{"annotation:gateway.istio.io/name-override"},
			Run: func(t framework.TestContext, s *Scenario) {
				name := s.Name("override")
				gw := s.NewGateway(t, GatewayOptions{Annotations: map[string]string{"gateway.istio.io/name-override": name}})
				EventuallyKube(t, func() error {
					d, err := gw.Deployment(t)
					if err != nil {
						return err
					}
					svc, err := gw.Service(t)
					if err != nil {
						return err
					}
					if d.Name != name || svc.Name != name {
						return fmt.Errorf("got deployment %q service %q, want %q", d.Name, svc.Name, name)
					}
					return nil
				})
			},
		},
		Feature{
			ID:          "infra.service-account-annotation",
			Description: "The gateway.istio.io/service-account annotation sets the gateway pods' service account (and thus mesh identity).",
			Roles:       ingressRole,
			Covers:      []string{"annotation:gateway.istio.io/service-account"},
			Run: func(t framework.TestContext, s *Scenario) {
				sa := s.Name("sa")
				gw := s.NewGateway(t, GatewayOptions{Annotations: map[string]string{"gateway.istio.io/service-account": sa}})
				if got := gw.ServiceAccount(t); got != sa {
					t.Fatalf("pod service account %q, want %q", got, sa)
				}
				s.Apply(t, httpRouteTo(gw, "{{.BackendV1}}", 80))
				gw.Expect(t, Request{}, RequestHeaderContains("X-Forwarded-Client-Cert", "/sa/"+sa))
			},
		},
		Feature{
			ID:          "infra.service-ports-from-listeners",
			Description: "The generated Service exposes one port per distinct listener port.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{Listeners: `
  - {name: http, port: 80, protocol: HTTP}
  - {name: alt, port: 8080, protocol: HTTP}
  - {name: tcp, port: 9090, protocol: TCP}
`})
				EventuallyKube(t, func() error {
					svc, err := gw.Service(t)
					if err != nil {
						return err
					}
					have := map[int32]bool{}
					for _, p := range svc.Spec.Ports {
						have[p.Port] = true
					}
					for _, want := range []int32{80, 8080, 9090} {
						if !have[want] {
							return fmt.Errorf("service ports %v missing %d", svc.Spec.Ports, want)
						}
					}
					return nil
				})
			},
		},
		Feature{
			ID:          "infra.owner-references",
			Description: "Generated Deployment and Service are owned by the Gateway (garbage-collected with it).",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				EventuallyKube(t, func() error {
					d, err := gw.Deployment(t)
					if err != nil {
						return err
					}
					svc, err := gw.Service(t)
					if err != nil {
						return err
					}
					for what, refs := range map[string][]metav1.OwnerReference{"deployment": d.OwnerReferences, "service": svc.OwnerReferences} {
						if !ownedByGateway(refs, gw.Name) {
							return fmt.Errorf("%s not owned by gateway %s: %+v", what, gw.Name, refs)
						}
					}
					return nil
				})
			},
		},
		Feature{
			ID:          "infra.standard-labels",
			Description: "Gateway pods carry the standard gateway-name and gateway-class-name labels.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				EventuallyKube(t, func() error {
					pods := gw.Pods(t)
					if len(pods) == 0 {
						return fmt.Errorf("no pods")
					}
					if got := pods[0].Labels["gateway.networking.k8s.io/gateway-class-name"]; got != s.Proxy.GatewayClass {
						return fmt.Errorf("gateway-class-name label %q, want %q", got, s.Proxy.GatewayClass)
					}
					return nil
				})
			},
		},
		Feature{
			ID:          "infra.reconcile-drift",
			Description: "Generated resources removed out-of-band (the gateway Service) are recreated by the controller.",
			Roles:       ingressRole,
			Run: func(t framework.TestContext, s *Scenario) {
				gw := s.NewGateway(t, GatewayOptions{})
				svc, err := gw.Service(t)
				if err != nil {
					t.Fatal(err)
				}
				if err := t.Clusters().Default().Kube().CoreV1().Services(svc.Namespace).Delete(context.Background(), svc.Name, metav1.DeleteOptions{}); err != nil {
					t.Fatal(err)
				}
				EventuallyKube(t, func() error {
					n, err := gw.Service(t)
					if err != nil {
						return err
					}
					if n.UID == svc.UID {
						return fmt.Errorf("service not yet recreated")
					}
					return nil
				})
			},
		},
		Feature{
			ID:          "infra.addresses-hostname-manual-deployment",
			Description: "A Gateway with a Hostname address binds to an existing gateway Service's pods instead of deploying its own.",
			Roles:       ingressRole,
			Covers:      []string{"gateway-api-ext:addresses-hostname-manual-deployment"},
			Run: func(t framework.TestContext, s *Scenario) {
				base := s.NewGateway(t, GatewayOptions{Suffix: "base"})
				svc, err := base.Service(t)
				if err != nil {
					t.Fatal(err)
				}
				manual := s.NewGateway(t, GatewayOptions{Suffix: "manual", Addresses: `
  - type: Hostname
    value: ` + svc.Name + `
`})
				if d, err := manual.Deployment(t); err == nil {
					t.Fatalf("manual gateway unexpectedly got its own deployment %s", d.Name)
				}
				s.Apply(t, httpRouteTo(manual, "{{.BackendV2}}", 80))
				base.Expect(t, Request{}, Version("v2"))
			},
		},
	)
}

func ownedByGateway(refs []metav1.OwnerReference, name string) bool {
	for _, r := range refs {
		if r.Kind == "Gateway" && r.Name == name {
			return true
		}
	}
	return false
}
