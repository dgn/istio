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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/crd"
)

// TestWaypointConformance runs every waypoint feature against every waypoint implementation,
// through a waypoint attached to the destination service.
func TestWaypointConformance(t *testing.T) {
	runConformance(t, RoleWaypoint, WaypointProxies)
}

// TestNodeConformance runs L4 features against ztunnel. There is no waypoint in the path.
func TestNodeConformance(t *testing.T) {
	runConformance(t, RoleNode, NodeProxies)
}

// TestSidecarConformance runs mesh features that the sidecar serves against every sidecar
// implementation. In ambient the same behaviors are covered by the node (L4) and waypoint (L7) roles.
func TestSidecarConformance(t *testing.T) {
	runConformance(t, RoleSidecar, SidecarProxies)
}

// runConformance runs every feature tagged with the role against each proxy of the role. Subtests are
// named <proxy>/<feature-id>, with the runs used for classification (run, rerun, control) below it.
// Each result is classified (see classify_test.go) and compared with the proxy's declared support
// manifest: a subtest fails on a regression, on a stale declaration, or on a result that cannot be
// trusted (flaky, indeterminate, an ignored security feature); known gaps are skipped, so the suite is
// green for an implementation that is exactly as conformant as it declares. A report of all outcomes
// is written to pcs-report-<role>.yaml.
func runConformance(t *testing.T, role Role, proxies []Proxy) {
	framework.NewTest(t).Run(func(t framework.TestContext) {
		crd.DeployGatewayAPIOrSkip(t)
		t.Cleanup(func() { writeReport(t, role) })
		for _, p := range selectedProxies(proxies) {
			p := p
			t.NewSubTest(p.Name).Run(func(t framework.TestContext) {
				if p.Mode == ModeAmbient && !ambientInstalled(t) {
					t.Skip("ambient is not installed (node/waypoint/ambient suites need ztunnel and CNI)")
				}
				if p.Role == RoleEgress && !egressInstalled(t) {
					t.Skip("egress gateway is not installed")
				}
				features := featuresFor(role)
				decl := loadDeclarations(t, role, p.Name)
				if *updateDeclared {
					// Cleanup runs after all (parallel) feature subtests have finished.
					t.Cleanup(func() {
						if err := decl.write(); err != nil {
							t.Errorf("updating %s: %v", decl.path, err)
						}
					})
				}
				base := &Scenario{Proxy: p, Env: pcs, Feature: Feature{ID: "shared"}}
				// One waypoint per proxy, shared by its features.
				if p.needsWaypoint() {
					base.waypoint = base.DeployWaypoint(t)
				}
				if p.Role == RoleIngress {
					// The shared gateway is created once per proxy, for route-level features.
					base.shared = base.NewGateway(t, GatewayOptions{})
				}
				// Mesh-wide features (MeshConfig, IstiodEnv) run one at a time after the parallel
				// ones, under the same <proxy>/<feature-id> names (see meshwide_test.go).
				parallel, serial := splitMeshWide(features)
				gate := newSerialGate()
				t.Cleanup(func() {
					gate.close()
					if err := istiod.ensureEnv(t, nil); err != nil {
						t.Errorf("restoring istiod environment: %v", err)
					}
				})
				for _, f := range parallel {
					f := f
					t.NewSubTest(f.ID).RunParallel(func(t framework.TestContext) {
						gate.enterParallel()
						defer gate.exitParallel()
						runFeature(t, f, base, decl)
					})
				}
				for _, f := range serial {
					f := f
					t.NewSubTest(f.ID).RunParallel(func(t framework.TestContext) {
						gate.enterSerial(envKey(f.IstiodEnv))
						defer gate.exitSerial()
						runFeature(t, f, base, decl)
					})
				}
			})
		}
	})
}

// ambientInstalled reports whether ambient is installed in the cluster. ztunnel only exists when it
// is, regardless of how the suite was configured.
func ambientInstalled(t framework.TestContext) bool {
	_, err := t.Clusters().Default().Kube().AppsV1().DaemonSets("istio-system").
		Get(context.Background(), "ztunnel", metav1.GetOptions{})
	return err == nil
}

// egressInstalled reports whether the egress gateway is deployed in the cluster. It is a separate
// deployment from istiod, so it can be absent even though the suite installed Istio itself.
func egressInstalled(t framework.TestContext) bool {
	cfg := inst.Settings()
	pods, err := t.Clusters().Default().Kube().CoreV1().Pods(cfg.EgressGatewayServiceNamespace).
		List(context.Background(), metav1.ListOptions{LabelSelector: "istio=" + cfg.EgressGatewayIstioLabel})
	return err == nil && len(pods.Items) > 0
}
