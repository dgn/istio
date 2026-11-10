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

package defaults

import (
	"testing"

	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"istio.io/istio/pilot/pkg/util/protoconv"
	"istio.io/istio/pkg/test/util/assert"
)

// TestDecide pins the decision-key semantics the inventory file relies on: the most specific key
// wins, so a proxy-specific feature decision can sit next to a "*/" todo for the other proxies.
func TestDecide(t *testing.T) {
	inv := Inventory{Decisions: map[string]string{
		"*/listener/name":                "exempt: naming",
		"*/cluster/metadata/*":           "exempt: metadata",
		"router/cluster/metadata/*":      "exempt: router metadata",
		"*/cluster/connect_timeout":      todo,
		"router/cluster/connect_timeout": "feature x",
	}}
	cases := []struct {
		path string
		want string
	}{
		{"sidecar/listener/name", "*/listener/name"},
		{"sidecar/listener/name2", ""},
		{"sidecar/cluster/metadata", "*/cluster/metadata/*"},
		{"sidecar/cluster/metadata/filter_metadata[*]", "*/cluster/metadata/*"},
		{"sidecar/cluster/metadata_x", ""},
		{"router/cluster/metadata/filter_metadata[*]", "router/cluster/metadata/*"},
		{"waypoint/cluster/connect_timeout", "*/cluster/connect_timeout"},
		{"router/cluster/connect_timeout", "router/cluster/connect_timeout"},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			assert.Equal(t, inv.decide(c.path), c.want)
		})
	}
}

// TestInventoryNormalization pins the walker rules on a hand-built listener: extensions keyed by
// name with their typed_config inlined, list indexes and values dropped, duplicates collapsed.
func TestInventoryNormalization(t *testing.T) {
	h := protoconv.MessageToAny(&hcm.HttpConnectionManager{
		StatPrefix:       "outbound_0.0.0.0_80",
		UseRemoteAddress: wrapperspb.Bool(true),
	})
	chain := func(name string) *listener.FilterChain {
		return &listener.FilterChain{
			Name: name,
			Filters: []*listener.Filter{{
				Name:       "envoy.filters.network.http_connection_manager",
				ConfigType: &listener.Filter_TypedConfig{TypedConfig: h},
			}},
		}
	}
	got := inventory("p/listener", []proto.Message{
		&listener.Listener{Name: "a", FilterChains: []*listener.FilterChain{chain("a1"), chain("a2")}},
		&listener.Listener{Name: "b", FilterChains: []*listener.FilterChain{chain("b1")}},
	})
	assert.Equal(t, got, []string{
		"p/listener/filter_chains/filters[envoy.filters.network.http_connection_manager]/stat_prefix",
		"p/listener/filter_chains/filters[envoy.filters.network.http_connection_manager]/use_remote_address",
		"p/listener/filter_chains/name",
		"p/listener/name",
	})
}
