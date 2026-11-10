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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/rand"

	"istio.io/istio/pkg/test/echo/common/scheme"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
	"istio.io/istio/pkg/test/util/retry"
)

// Install-time settings: MeshConfig fields and istiod feature flags (Feature.MeshConfig,
// Feature.IstiodEnv). They change the whole mesh, so the runner runs these features one at a time
// after the parallel ones (meshwide_test.go). Mesh config has no direct observable: every feature
// asserts a positive effect of its patch with a retrying check, which is what synchronizes it with
// the push (the baseline without the patch is the opposite, which the negative controls verify).
func init() {
	Register(
		Feature{
			ID:          "meshconfig.default-http-retry-policy",
			Description: "MeshConfig defaultHttpRetryPolicy (attempts, retryOn) retries failed requests on routes without their own retry policy.",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"MeshConfig.defaultHttpRetryPolicy", "MeshConfig.defaultHttpRetryPolicy.attempts", "MeshConfig.defaultHttpRetryPolicy.retryOn"},
			// Istio's default policy (restored by the suite) does not retry a 500; with 5xx and 10
			// attempts, a request answered 500 half of the time fails with probability 2^-11.
			MeshConfig: `
defaultHttpRetryPolicy:
  attempts: 10
  retryOn: 5xx
`,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				// Without the policy about half of the requests fail; with it, at most 3 of 50 may
				// (expected 0.02), a false failure rate below 1e-7.
				tg.Expect(t, Request{Path: "/?codes=500:1,200:1", Count: 50}, MaxFailures(3))
			},
		},
		Feature{
			ID:          "meshconfig.enable-auto-mtls-disabled",
			Description: "MeshConfig enableAutoMtls: false makes the proxy send plaintext to a meshed backend that has no DestinationRule: the backend sees no client certificate (X-Forwarded-Client-Cert).",
			Roles:       []Role{RoleIngress, RoleSidecar},
			RequiresL7:  true,
			Covers:      []string{"MeshConfig.enableAutoMtls"},
			// The backend's sidecar adds X-Forwarded-Client-Cert only for mTLS connections, so the
			// header is the observable; it is present by default (backend.mesh.auto-mtls). Waypoint
			// and node traffic always uses HBONE (mTLS), so the field does not apply there.
			MeshConfig: `
enableAutoMtls: false
`,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				tg.Expect(t, Request{}, check.And(check.OK(), RequestHeaderAbsent("X-Forwarded-Client-Cert")))
			},
		},
		Feature{
			ID:          "meshconfig.h2-upgrade-policy",
			Description: "MeshConfig h2UpgradePolicy UPGRADE makes the proxy talk HTTP/2 to HTTP/1.1-declared backends.",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers:      []string{"MeshConfig.h2UpgradePolicy", "MeshConfig.h2UpgradePolicy=UPGRADE"},
			MeshConfig: `
h2UpgradePolicy: UPGRADE
`,
			Run: func(t framework.TestContext, s *Scenario) {
				// Out-of-mesh backend: an in-mesh sidecar would re-encode the request toward the app.
				tg := s.Target(t, TargetOptions{Routing: RouteNone})
				s.Apply(t, externalRoute(tg.Gateway()))
				tg.Expect(t, Request{}, check.And(check.OK(), check.Protocol("HTTP/2.0")))
			},
		},
		Feature{
			ID:          "meshconfig.access-log-encoding-json",
			Description: "MeshConfig accessLogEncoding JSON makes the proxy write access logs as JSON objects.",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers:      []string{"MeshConfig.accessLogEncoding", "MeshConfig.accessLogEncoding=JSON"},
			MeshConfig: `
accessLogEncoding: JSON
`,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				expectAccessLogLine(t, tg, func(marker, line string) error {
					var entry map[string]any
					if err := json.Unmarshal([]byte(line), &entry); err != nil {
						return fmt.Errorf("access log line is not JSON (%v): %s", err, line)
					}
					if entry["path"] != marker {
						return fmt.Errorf("JSON access log has path %v, want %s: %s", entry["path"], marker, line)
					}
					return nil
				})
			},
		},
		Feature{
			ID:          "meshconfig.access-log-format",
			Description: "MeshConfig accessLogFormat sets the text format of access log lines.",
			Roles:       ingressRole,
			RequiresL7:  true,
			Covers:      []string{"MeshConfig.accessLogFormat"},
			MeshConfig: `
accessLogFormat: "pcs-format path=%REQ(:PATH)% code=%RESPONSE_CODE%\n"
`,
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				expectAccessLogLine(t, tg, func(marker, line string) error {
					if want := "pcs-format path=" + marker + " code=200"; !strings.Contains(line, want) {
						return fmt.Errorf("access log line does not use the configured format (want %q): %s", want, line)
					}
					return nil
				})
			},
		},
		Feature{
			ID:          "flag.pilot-http10",
			Description: "istiod flag PILOT_HTTP10 makes the proxy accept HTTP/1.0 requests (rejected with 426 by default).",
			Roles:       l7Roles,
			RequiresL7:  true,
			Covers:      []string{"flag:PILOT_HTTP10"},
			IstiodEnv:   map[string]string{"PILOT_HTTP10": "true"},
			Run: func(t framework.TestContext, s *Scenario) {
				tg := s.Target(t, TargetOptions{})
				// A raw HTTP/1.0 request over TCP (the echo client only speaks HTTP/1.1 and 2). The
				// echo client appends the final "\n" and fails the call unless the response is the
				// echo server's 200; without HTTP/1.0 support the proxy answers 426.
				http10 := "GET / HTTP/1.0\r\nHost: " + tg.Host() + "\r\n\r"
				tg.Expect(t, Request{Scheme: scheme.TCP, Opts: func(o *echo.CallOptions) { o.Message = http10 }}, check.NoError())
			},
		},
	)
}

// expectAccessLogLine sends requests with a fresh marker path through a gateway target until the
// gateway's access log line for one of them satisfies check. A request sent before a mesh config
// change reached the proxy is logged the old way, so each attempt uses a new marker: the retry is the
// propagation witness.
func expectAccessLogLine(t framework.TestContext, tg *Target, check func(marker, line string) error) {
	t.Helper()
	gw := tg.Gateway()
	retry.UntilSuccessOrFail(t, func() error {
		marker := "/pcs-log-" + rand.String(10)
		if _, err := tg.Call(t, Request{Path: marker}); err != nil {
			return err
		}
		var line string
		err := retry.UntilSuccess(func() error {
			logs, err := gw.Logs(t)
			if err != nil {
				return err
			}
			for _, l := range strings.Split(logs, "\n") {
				if strings.Contains(l, marker) {
					line = l
					return nil
				}
			}
			return fmt.Errorf("gateway did not log %s", marker)
		}, retry.Timeout(logFlushBound), retry.Delay(time.Second))
		if err != nil {
			return err
		}
		return check(marker, strings.TrimSpace(line))
	}, retry.Timeout(2*time.Minute), retry.Delay(time.Second))
}
