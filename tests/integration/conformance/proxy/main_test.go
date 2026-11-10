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

// Package proxy contains the Istio Proxy Conformance Suite (PCS).
//
// The suite runs one set of implementation-neutral, black-box feature tests against every proxy
// implementation Istio supports in a given role, so that we get definitive data about which
// features each implementation supports. See architecture/tests/proxy-conformance.md.
//
// The suite covers every role the mesh uses proxies in: ingress gateways, egress gateways,
// waypoints, the node proxy (ztunnel) and sidecars. Each feature is run against every proxy that
// implements its role (for ingress: Envoy via GatewayClass "istio", agentgateway via GatewayClass
// "istio-agentgateway"). Use --istio.test.pcs.proxies to restrict the set, and -run to select
// features, e.g.:
//
//	go test -tags=integ -run 'TestIngressConformance/agentgateway/security' \
//	  ./tests/integration/conformance/proxy/...
package proxy

import (
	"path/filepath"
	"testing"

	"istio.io/istio/pkg/test/env"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/authz"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/label"
	"istio.io/istio/pkg/test/framework/resource"
	"istio.io/istio/pkg/test/framework/resource/config/apply"
)

var (
	inst     istio.Instance
	pcs      = &Env{}
	extAuthz authz.Server
)

func TestMain(m *testing.M) {
	framework.
		NewSuite(m).
		Label(label.CustomSetup).
		// Install Gateway API CRDs (incl. experimental kinds like TCPRoute/TLSRoute/ListenerSet)
		// before istiod starts, so all Gateway API controllers see them from the beginning.
		Setup(func(ctx resource.Context) error {
			return ctx.ConfigIstio().
				File("", filepath.Join(env.IstioSrc, "tests/integration/pilot/testdata/gateway-api-crd.yaml")).
				Apply(apply.NoCleanup)
		}).
		// Ambient (ztunnel + CNI) is part of the install so the node, waypoint and ambient dataplane
		// suites can run. Suites that need it skip when the suite runs against an installation
		// without ambient.
		Setup(func(ctx resource.Context) error {
			ctx.Settings().Ambient = true
			return nil
		}).
		Setup(istio.Setup(&inst, func(ctx resource.Context, cfg *istio.Config) {
			cfg.DeployGatewayAPI = true
			cfg.EnableCNI = true
			// The suite is single-cluster; the eastwest gateway is not needed and only slows the
			// install down (and fails on setups that cannot run it).
			cfg.DeployEastWestGW = false
			// The suite measures Istio's product defaults. tests/integration/base.yaml disables the
			// default retry policy for all integration tests; restore Istio's built-in default
			// (pilot/pkg/networking/core/route/retry/retry.go) so default-retry behavior is observable.
			cfg.ControlPlaneValues = `
meshConfig:
  accessLogFile: /dev/stdout
  defaultHttpRetryPolicy:
    attempts: 2
    retryOn: connect-failure,refused-stream,unavailable,cancelled,retriable-status-codes
values:
  pilot:
    env:
      PILOT_ENABLE_AGENTGATEWAY: "true"
      PILOT_ENABLE_ALPHA_GATEWAY_API: "true"
`
		})).
		Setup(pcs.Setup).
		Setup(authz.Setup(&extAuthz, nil)).
		Run()
}
