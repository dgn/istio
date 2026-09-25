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

// Package bookinfo holds helpers shared by the Bookinfo demo services.
package bookinfo

import (
	"encoding/json"
	"net/http"
	"os"
)

// ForwardedHeaders is the set of tracing-related headers propagated from an
// incoming request to outgoing requests, so the Istio proxies can tie
// together the spans of a single distributed trace: x-request-id plus the
// B3 and W3C Trace Context schemes used by Istio.
var ForwardedHeaders = []string{
	"x-request-id",
	"traceparent",
	"tracestate",
	"x-b3-traceid",
	"x-b3-spanid",
	"x-b3-parentspanid",
	"x-b3-sampled",
	"x-b3-flags",
}

// Forward copies the headers in ForwardedHeaders from src onto the outgoing
// request dst. Header names are matched case-insensitively.
func Forward(src, dst *http.Request) {
	for _, name := range ForwardedHeaders {
		for _, v := range src.Header.Values(name) {
			dst.Header.Add(name, v)
		}
	}
}

// WriteJSON writes v as a JSON response with the given status code. HTML
// characters are not escaped, matching the original services' output.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// EnvOr returns the value of the environment variable key, or def if it is
// not set (or empty).
func EnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
