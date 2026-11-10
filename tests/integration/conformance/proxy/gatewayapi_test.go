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
	"io/fs"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	v1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/gateway-api/conformance"
	"sigs.k8s.io/gateway-api/conformance/tests"
	"sigs.k8s.io/gateway-api/conformance/utils/suite"
	"sigs.k8s.io/gateway-api/pkg/features"

	"istio.io/istio/pkg/kube"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/crd"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/scopes"
	"istio.io/istio/pkg/test/util/assert"
)

// gatewayAPIConformanceNamespaces are defined in sigs.k8s.io/gateway-api/conformance/base/manifests.yaml.
var gatewayAPIConformanceNamespaces = []string{
	"gateway-conformance-infra",
	"gateway-conformance-app-backend",
	"gateway-conformance-web-backend",
}

// TestGatewayAPIConformance runs the upstream Gateway API conformance suite against every gateway
// proxy, with *all* Gateway (non-mesh) features enabled. Unlike the runners in
// tests/integration/pilot, which only enable the features each implementation claims, this gives
// definitive data: a failing test means the feature is not supported by that proxy.
//
// Subtests are named <proxy>/<upstream test name>.
func TestGatewayAPIConformance(t *testing.T) {
	framework.NewTest(t).Run(func(ctx framework.TestContext) {
		crd.DeployGatewayAPIOrSkip(ctx)
		if ctx.Settings().Image.PullSecret != "" {
			for _, ns := range gatewayAPIConformanceNamespaces {
				namespace.Claim(ctx, namespace.Config{Prefix: ns, Inject: false})
			}
		}

		kc := ctx.Clusters().Default()
		clientOptions := client.Options{Scheme: kube.IstioScheme}
		c, err := client.New(kc.RESTConfig(), clientOptions)
		assert.NoError(t, err)

		all := features.SetsToNamesSet(features.AllFeatures)
		var gatewayFeatures []features.FeatureName
		for _, f := range all.UnsortedList() {
			if !strings.HasPrefix(string(f), "Mesh") {
				gatewayFeatures = append(gatewayFeatures, f)
			}
		}

		hostnameType := v1.AddressType("Hostname")
		for _, p := range selectedProxies(IngressProxies) {
			// The upstream suite needs a *testing.T and shares fixed namespaces, so proxies run
			// sequentially as plain Go subtests.
			t.Run(p.Name, func(t *testing.T) {
				opts := suite.ConformanceOptions{
					ConfigurableOptions: suite.ConfigurableOptions{
						GatewayClassName:         p.GatewayClass,
						Debug:                    scopes.Framework.DebugEnabled(),
						CleanupBaseResources:     !ctx.Settings().NoCleanup,
						CleanupTestResources:     !ctx.Settings().NoCleanup,
						SupportedFeatures:        gatewayFeatures,
						UsableNetworkAddresses:   []v1.GatewaySpecAddress{{Value: "infra-backend-v1.gateway-conformance-infra.svc.cluster.local", Type: &hostnameType}},
						UnusableNetworkAddresses: []v1.GatewaySpecAddress{{Value: "foo", Type: &hostnameType}},
						ConformanceProfiles: []suite.ConformanceProfileName{
							suite.GatewayHTTPConformanceProfileName,
							suite.GatewayTLSConformanceProfileName,
							suite.GatewayGRPCConformanceProfileName,
							suite.GatewayTCPConformanceProfileName,
						},
						TimeoutConfig:     ctx.Settings().GatewayConformanceTimeoutConfig,
						AllowCRDsMismatch: ctx.Settings().GatewayConformanceAllowCRDsMismatch,
					},
					Client:        c,
					Clientset:     kc.Kube(),
					ClientOptions: clientOptions,
					RestConfig:    kc.RESTConfig(),
					ManifestFS:    []fs.FS{&conformance.Manifests},
				}
				opts.NamespaceLabels = map[string]string{"istio-injection": "enabled"}
				if rev := ctx.Settings().Revisions.Default(); rev != "" {
					opts.NamespaceLabels = map[string]string{"istio.io/rev": rev}
				}
				csuite, err := suite.NewConformanceTestSuite(opts)
				assert.NoError(t, err)
				csuite.Setup(t, tests.ConformanceTests)
				assert.NoError(t, csuite.Run(t, tests.ConformanceTests))
			})
		}
	})
}
