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
)

// TestIngressConformance runs every ingress feature against every ingress gateway proxy.
func TestIngressConformance(t *testing.T) {
	runConformance(t, RoleIngress, IngressProxies)
}

// TestEgressConformance runs every egress feature against every egress gateway proxy. It skips when
// the suite runs against an installation without an egress gateway.
func TestEgressConformance(t *testing.T) {
	runConformance(t, RoleEgress, EgressProxies)
}
