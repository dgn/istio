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
	"strings"
	"time"

	echoClient "istio.io/istio/pkg/test/echo"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/check"
	"istio.io/istio/pkg/test/util/retry"
	"istio.io/istio/tests/integration/conformance/proxy/outcome"
)

// H builds headers from key/value pairs.
func H(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return h
}

// Version checks that every response came from the given backend version.
func Version(v string) echo.Checker {
	return check.And(check.OK(), check.Each(func(r echoClient.Response) error {
		if r.Version != v {
			return fmt.Errorf("expected version %q, got %q", v, r.Version)
		}
		return nil
	}))
}

// FromService checks that every response came from a pod of the given service. Echo pod
// hostnames are <service>-<version>-<hash>.
func FromService(svc string) echo.Checker {
	return check.And(check.OK(), check.Each(func(r echoClient.Response) error {
		if !strings.HasPrefix(r.Hostname, svc+"-") {
			return fmt.Errorf("expected response from %q, got hostname %q", svc, r.Hostname)
		}
		return nil
	}))
}

// Path checks the request path (including query) seen by the backend.
func Path(p string) echo.Checker {
	return check.And(check.OK(), check.Each(func(r echoClient.Response) error {
		if r.URL != p {
			return fmt.Errorf("expected backend to see path %q, got %q", p, r.URL)
		}
		return nil
	}))
}

// BackendHost checks the Host/:authority seen by the backend.
func BackendHost(h string) echo.Checker {
	return check.And(check.OK(), check.Host(h))
}

// RequestHeaderAbsent checks that the backend did not receive the header.
func RequestHeaderAbsent(key string) echo.Checker {
	return check.And(check.OK(), check.Each(func(r echoClient.Response) error {
		if v := r.RequestHeaders.Values(key); len(v) > 0 {
			return fmt.Errorf("expected request header %q to be absent, got %v", key, v)
		}
		return nil
	}))
}

// RequestHeaderPresent checks that the backend received the header, with any value.
func RequestHeaderPresent(key string) echo.Checker {
	return check.And(check.OK(), check.Each(func(r echoClient.Response) error {
		if r.RequestHeaders.Get(key) == "" {
			return fmt.Errorf("expected request header %q to be present", key)
		}
		return nil
	}))
}

// RequestHeaderContains checks that the backend received the header containing a substring.
func RequestHeaderContains(key, sub string) echo.Checker {
	return check.And(check.OK(), check.Each(func(r echoClient.Response) error {
		if v := strings.Join(r.RequestHeaders.Values(key), ","); !strings.Contains(v, sub) {
			return fmt.Errorf("expected request header %q to contain %q, got %q", key, sub, v)
		}
		return nil
	}))
}

// ResponseHeaderAbsent checks that the client did not receive the response header.
func ResponseHeaderAbsent(key string) echo.Checker {
	return check.Each(func(r echoClient.Response) error {
		if v := r.ResponseHeaders.Values(key); len(v) > 0 {
			return fmt.Errorf("expected response header %q to be absent, got %v", key, v)
		}
		return nil
	})
}

// ResponseHeaderPresent checks that the client received the response header, with any value.
func ResponseHeaderPresent(key string) echo.Checker {
	return check.Each(func(r echoClient.Response) error {
		if r.ResponseHeaders.Get(key) == "" {
			return fmt.Errorf("expected response header %q to be present", key)
		}
		return nil
	})
}

// Distribution checks that responses are split across versions by the given weights (percent). The
// tolerance is derived from the number of responses and a fixed false-failure rate (see
// outcome.CheckShare), so send at least outcome.SampleSize(weights...) requests: the check fails as
// "sample too small" otherwise. Prefer a deterministic design where the behavior allows it (0/100
// then 100/0, header-based steering); keep statistical checks for proportional splits.
func Distribution(weights map[string]int) echo.Checker {
	return check.And(check.OK(), func(result echo.CallResult, _ error) error {
		total := len(result.Responses)
		if total == 0 {
			return fmt.Errorf("no responses")
		}
		got := map[string]int{}
		for _, r := range result.Responses {
			got[r.Version]++
		}
		for v, w := range weights {
			if err := outcome.CheckShare("version "+v, got[v], total, w); err != nil {
				return fmt.Errorf("%v; all: %v", err, got)
			}
		}
		return nil
	})
}

// SampleSize is the request count for a statistical check of the given shares (percent).
func SampleSize(weights ...int) int {
	return outcome.SampleSize(weights...)
}

// ResponseHeaderContains checks that the client received a response header containing sub.
func ResponseHeaderContains(key, sub string) echo.Checker {
	return check.Each(func(r echoClient.Response) error {
		if v := strings.Join(r.ResponseHeaders.Values(key), ","); !strings.Contains(v, sub) {
			return fmt.Errorf("expected response header %q to contain %q, got %q", key, sub, v)
		}
		return nil
	})
}

// MaxFailures checks that at most n responses were not 200.
func MaxFailures(n int) echo.Checker {
	return func(result echo.CallResult, err error) error {
		if len(result.Responses) == 0 {
			return fmt.Errorf("no responses: %v", err)
		}
		failed := 0
		for _, r := range result.Responses {
			if r.Code != "200" {
				failed++
			}
		}
		if failed > n {
			return fmt.Errorf("%d of %d requests failed, expected at most %d", failed, len(result.Responses), n)
		}
		return nil
	}
}

// expectMirrored waits until the backend workloads of the given version have logged a request
// containing marker.
func expectMirrored(t framework.TestContext, backends echo.Instances, version, marker string) {
	t.Helper()
	retry.UntilSuccessOrFail(t, func() error {
		for _, inst := range backends {
			ws, err := inst.Workloads()
			if err != nil {
				return err
			}
			for _, w := range ws {
				if !strings.Contains(w.PodName(), "-"+version+"-") {
					continue
				}
				logs, err := w.Logs()
				if err != nil {
					return err
				}
				if strings.Contains(logs, marker) {
					return nil
				}
			}
		}
		return fmt.Errorf("no %s workload logged mirrored request %q", version, marker)
	}, retry.Timeout(30*time.Second))
}

// headerNotContains checks that the backend's request header does not contain sub.
func headerNotContains(key, sub string) echo.Checker {
	return check.Each(func(r echoClient.Response) error {
		if v := strings.Join(r.RequestHeaders.Values(key), ","); strings.Contains(v, sub) {
			return fmt.Errorf("expected request header %q not to contain %q, got %q", key, sub, v)
		}
		return nil
	})
}

func upper(s string) string { return strings.ToUpper(s) }

// Denied checks that the request was denied: either with 401/403, or with no response at all. An L4
// proxy (ztunnel) can only reset the connection, it cannot produce an HTTP status.
func Denied() echo.Checker {
	return func(result echo.CallResult, err error) error {
		if err != nil || len(result.Responses) == 0 {
			return nil
		}
		for _, r := range result.Responses {
			if r.Code != "401" && r.Code != "403" {
				return fmt.Errorf("expected denial (401/403 or connection closed), got %s", r.Code)
			}
		}
		return nil
	}
}

// Rejected checks that requests were refused for authentication/authorization reasons (401 or 403).
// Implementations legitimately differ in which code they use for some failures (e.g. JWT audience
// mismatch).
func Rejected() echo.Checker {
	return check.Each(func(r echoClient.Response) error {
		if r.Code != "401" && r.Code != "403" {
			return fmt.Errorf("expected 401 or 403, got %s", r.Code)
		}
		return nil
	})
}

// statusShare checks that pct percent of responses have the code, within the tolerance derived from
// the number of responses (see Distribution). Send at least SampleSize(pct) requests.
func statusShare(code, pct int) echo.Checker {
	return func(result echo.CallResult, _ error) error {
		if len(result.Responses) == 0 {
			return fmt.Errorf("no responses")
		}
		n := 0
		for _, r := range result.Responses {
			if r.Code == fmt.Sprint(code) {
				n++
			}
		}
		return outcome.CheckShare(fmt.Sprintf("status %d", code), n, len(result.Responses), pct)
	}
}
