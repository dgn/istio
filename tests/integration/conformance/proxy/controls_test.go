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
	"testing"

	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/crd"
)

// TestIngressConformanceControls validates the *tests*, not the proxies. Every ingress feature is run
// against the reference implementation (Envoy) with its config under test removed (see
// featureConfigKinds and NewGateway). A valid test must then fail: if all its assertions still pass,
// the test cannot tell "supported" from "not supported" and this control fails, naming the weak
// feature.
func TestIngressConformanceControls(t *testing.T) {
	runControls(t, RoleIngress, IngressProxies)
}

// TestEgressConformanceControls is the negative-control counterpart for the egress features.
func TestEgressConformanceControls(t *testing.T) {
	runControls(t, RoleEgress, EgressProxies)
}

// TestSidecarConformanceControls is the negative-control counterpart for the mesh features the
// sidecar serves, run against the reference sidecar implementation.
func TestSidecarConformanceControls(t *testing.T) {
	runControls(t, RoleSidecar, SidecarProxies)
}

// TestWaypointConformanceControls is the negative-control counterpart for the waypoint features,
// run against the reference waypoint implementation.
func TestWaypointConformanceControls(t *testing.T) {
	runControls(t, RoleWaypoint, WaypointProxies)
}

// runControls runs every feature of the role against the role's reference implementation with the
// feature config stripped. Features that apply no strippable config (e.g. data-plane defaults) are
// skipped as not applicable.
func runControls(t *testing.T, role Role, proxies []Proxy) {
	framework.NewTest(t).Run(func(t framework.TestContext) {
		crd.DeployGatewayAPIOrSkip(t)
		ref := proxies[0]
		t.NewSubTest(ref.Name).Run(func(t framework.TestContext) {
			if ref.Role == RoleEgress && !egressInstalled(t) {
				t.Skip("egress gateway is not installed")
			}
			if ref.Mode == ModeAmbient && !ambientInstalled(t) {
				t.Skip("ambient is not installed")
			}
			features := featuresFor(role)
			base := &Scenario{Proxy: ref, Env: pcs, Feature: Feature{ID: "shared-control"}}
			if ref.Role == RoleIngress {
				// Shared by all features' scenarios, as in the real run.
				base.shared = base.NewGateway(t, GatewayOptions{})
			}
			if ref.needsWaypoint() {
				base.waypoint = base.DeployWaypoint(t)
			}
			for _, f := range features {
				f := f
				t.NewSubTest(f.ID).RunParallel(func(t framework.TestContext) {
					if f.NoControl != "" {
						t.Skip("negative control not applicable: " + f.NoControl)
					}
					s := &Scenario{Proxy: ref, Env: pcs, Feature: f, shared: base.shared, waypoint: base.waypoint, control: true}
					// MeshConfig/IstiodEnv are stripped, so controls never change mesh-wide state and
					// mesh-wide features can run in parallel here.
					s.applyMeshWide(t)
					r := &recorder{TestContext: t}
					r.run(func() { f.Run(r, s) })
					switch {
					case r.skipped != "":
						t.Skip("feature skipped: " + r.skipped)
					case !s.stripped:
						t.Skip("no feature config to remove; negative control not applicable")
					case !r.failed():
						t.Errorf("weak test: all assertions passed with the feature config removed")
					default:
						t.Logf("control OK: feature fails without its config at %s (%s)", r.failedAt(), r.firstFailure())
					}
				})
			}
		})
	})
}
