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

// Package defaults holds TestDefaultsInventory, the proxy conformance suite's check on behavior
// istiod configures without any user config (XFF handling, request IDs, retries, timeouts, HTTP/2
// settings, ...).
//
// The coverage package enumerates API fields; a proxy that implements every field can still differ
// from Envoy-as-configured-by-istiod in everything istiod sets by default. This package generates
// xDS for a minimal mesh with istiod's fake discovery server, walks every set field of the result
// into a normalized, value-free inventory, and requires a decision for each path in
// defaults-inventory.yaml, so a new default fails the check until someone decides about it, exactly
// like a new API field does in the coverage package.
//
// It is a separate package so the coverage package does not link istiod.
package defaults
