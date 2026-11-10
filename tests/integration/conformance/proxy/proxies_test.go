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
	"flag"
	"strings"

	"istio.io/istio/pkg/config/constants"
	"istio.io/istio/pkg/slices"
)

// Role is the position a proxy takes in the mesh.
type Role string

const (
	// RoleIngress is a north-south ingress gateway, a Gateway API gateway (class "istio" /
	// "istio-agentgateway").
	RoleIngress Role = "ingress"
	// RoleEgress is the egress gateway: traffic from meshed workloads to external hosts leaves the
	// mesh through it. It is an istio-proxy gateway (Istio API Gateway with selector
	// istio=egressgateway), not a Gateway API gateway, so a proxy implements this role separately
	// from RoleIngress.
	RoleEgress Role = "egress"
	// RoleWaypoint is an L7 proxy for ambient traffic (class "istio-waypoint" /
	// "istio-agentgateway-waypoint").
	RoleWaypoint Role = "waypoint"
	// RoleNode is the per-node proxy, ztunnel, which handles ambient traffic at L4.
	RoleNode Role = "node"
	// RoleSidecar is the per-workload proxy in sidecar mode.
	RoleSidecar Role = "sidecar"
)

// DataplaneMode is how workloads are attached to a workload proxy.
type DataplaneMode string

const (
	// ModeSidecar injects a sidecar into the workload's namespace.
	ModeSidecar DataplaneMode = "sidecar"
	// ModeAmbient labels the workload's namespace for ambient capture: ztunnel, and a waypoint for
	// features that need L7.
	ModeAmbient DataplaneMode = "ambient"
)

// Proxy is a proxy implementation in a role (a "proxy under test").
type Proxy struct {
	// Name identifies the implementation, e.g. "envoy". Used in test names.
	Name string
	Role Role
	// GatewayClass deploys this proxy as a Kubernetes Gateway API Gateway: an ingress gateway
	// (RoleIngress) or a waypoint (RoleWaypoint). Empty for roles that are not Gateway API
	// gateways (RoleEgress, RoleNode, RoleSidecar).
	GatewayClass string
	// Mode is how workloads are attached to a workload proxy (RoleSidecar, RoleNode, RoleWaypoint).
	// Egress features observe traffic from sidecar workloads, so it is ModeSidecar there.
	Mode DataplaneMode
}

// IngressProxies are all ingress gateway implementations Istio supports.
var IngressProxies = []Proxy{
	{Name: "envoy", Role: RoleIngress, GatewayClass: "istio"},
	{Name: "agentgateway", Role: RoleIngress, GatewayClass: constants.AgentgatewayClassName},
}

// EgressProxies are all egress gateway implementations Istio supports. The egress gateway is an
// istio-proxy deployment, so agentgateway does not implement this role.
var EgressProxies = []Proxy{
	{Name: "envoy", Role: RoleEgress, Mode: ModeSidecar},
}

// WaypointProxies are all waypoint implementations Istio supports.
var WaypointProxies = []Proxy{
	{Name: "envoy", Role: RoleWaypoint, Mode: ModeAmbient, GatewayClass: constants.WaypointGatewayClassName},
	{Name: "agentgateway", Role: RoleWaypoint, Mode: ModeAmbient, GatewayClass: constants.AgentgatewayWaypointClassName},
}

// NodeProxies are all node-proxy implementations Istio supports (ztunnel). There is no waypoint in
// this role: only ztunnel handles the traffic, so features are L4 only.
var NodeProxies = []Proxy{
	{Name: "ztunnel", Role: RoleNode, Mode: ModeAmbient},
}

// SidecarProxies are all sidecar implementations Istio supports.
var SidecarProxies = []Proxy{
	{Name: "envoy", Role: RoleSidecar, Mode: ModeSidecar},
}

// needsWaypoint reports whether every feature of this role goes through a waypoint.
func (p Proxy) needsWaypoint() bool {
	return p.Role == RoleWaypoint
}

// isGateway reports whether the proxy runs as a standalone gateway (ingress or egress) rather than
// attached to workloads. Gateways have their own config namespace; the others work on workloads.
func (p Proxy) isGateway() bool {
	return p.Role == RoleIngress || p.Role == RoleEgress
}

var proxyFilter = flag.String("istio.test.pcs.proxies", "",
	"Comma-separated list of proxy implementations to run the proxy conformance suite against "+
		"(e.g. 'envoy,agentgateway'). Defaults to all supported proxies.")

// selectedProxies returns the proxies matching --istio.test.pcs.proxies.
func selectedProxies(all []Proxy) []Proxy {
	if *proxyFilter == "" {
		return all
	}
	want := strings.Split(*proxyFilter, ",")
	return slices.FilterInPlace(slices.Clone(all), func(p Proxy) bool {
		return slices.Contains(want, p.Name)
	})
}
