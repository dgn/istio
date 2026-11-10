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

// Package gatewayext is the registry of Istio's extensions of Gateway API semantics: the
// Istio-specific option keys, annotations, labels and extra kinds that the Gateway API translators
// (gateway, agentgateway) and the deployment controller (gatewaycommon) honor on top of the upstream
// API. The translators take their keys from here, and the proxy conformance suite
// (tests/conformance/proxy/coverage) derives part of its API surface from Registry, so an
// extension added here is tracked automatically. It is a leaf package: keep its imports minimal.
package gatewayext

import (
	"istio.io/istio/pkg/config/constants"
)

// Keys of listener tls.options, annotations and labels.
const (
	// TLSTerminateModeOption selects the TLS mode of a Terminate listener (TLSTerminateModes).
	TLSTerminateModeOption = "gateway.istio.io/tls-terminate-mode"
	// TLSCipherSuitesOption restricts the cipher suites of a Terminate listener.
	TLSCipherSuitesOption = "gateway.istio.io/tls-cipher-suites"
	// ListenerProtocolOption overrides the listener protocol (e.g. ListenerModeAutoPassthrough).
	ListenerProtocolOption = constants.ListenerModeOption
	// AddressTypeAnnotation on a Gateway selects the address type of the generated Service.
	AddressTypeAnnotation = "networking.istio.io/address-type"
	// ProxyConfigAnnotation on a Gateway sets the ProxyConfig of the deployed proxy.
	ProxyConfigAnnotation = "proxy.istio.io/config"
	// ClassDefaultsLabel marks a ConfigMap holding the defaults of a GatewayClass.
	ClassDefaultsLabel = "gateway.istio.io/defaults-for-class"
	// HostnameBackendKind is the extra backendRef kind (group networking.istio.io) naming a host.
	HostnameBackendKind = "Hostname"
)

// TLS modes, the values of TLSTerminateModeOption.
const (
	TLSModeMutual         = "MUTUAL"
	TLSModeOptionalMutual = "OPTIONAL_MUTUAL"
	// TLSModeIstioSimple is simple TLS with the gateway's built-in workload certificate.
	TLSModeIstioSimple = "ISTIO_SIMPLE"
	TLSModeIstioMutual = "ISTIO_MUTUAL"
)

// TLSTerminateModes are the values of TLSTerminateModeOption.
var TLSTerminateModes = []string{TLSModeMutual, TLSModeOptionalMutual, TLSModeIstioSimple, TLSModeIstioMutual}

// ParametersRefOverlays are the keys of a parametersRef or GatewayClass-defaults ConfigMap that
// overlay the resources deployed for a Gateway.
var ParametersRefOverlays = []string{"deployment", "service", "serviceAccount", "horizontalPodAutoscaler", "podDisruptionBudget"}

// Extension is one extension, named as an entry of the proxy conformance API surface.
type Extension struct {
	Entry       string
	Description string
}

// Registry returns every extension.
func Registry() []Extension {
	out := []Extension{
		{"gateway-api-ext:tls-option:" + TLSCipherSuitesOption, "listener tls.options: cipher suites of a Terminate listener"},
		{
			"gateway-api-ext:tls-option:" + ListenerProtocolOption + "=" + constants.ListenerModeAutoPassthrough,
			"listener tls.options: SNI auto-passthrough to mesh services",
		},
		{"gateway-api-ext:backendref-kind:networking.istio.io/" + HostnameBackendKind, "backendRef kind naming a host (e.g. of a ServiceEntry)"},
		{"gateway-api-ext:tls-terminate-listener:TCPRoute", "TCPRoute attached to a TLS Terminate listener"},
		{"gateway-api-ext:gatewayclass-defaults-configmap:" + ClassDefaultsLabel, "ConfigMap with the defaults of a GatewayClass"},
		{"gateway-api-ext:addresses-hostname-manual-deployment", "a Hostname address binds the Gateway to an existing Service's pods"},
		{"annotation:" + ProxyConfigAnnotation, "Gateway annotation: ProxyConfig of the deployed proxy"},
		{"annotation:" + AddressTypeAnnotation, "Gateway annotation: address type of the generated Service"},
	}
	for _, m := range TLSTerminateModes {
		out = append(out, Extension{"gateway-api-ext:tls-option:" + TLSTerminateModeOption + "=" + m, "listener tls.options: TLS mode of a Terminate listener"})
	}
	for _, o := range ParametersRefOverlays {
		out = append(out, Extension{"gateway-api-ext:parametersref-overlay:" + o, "parametersRef/GatewayClass-defaults overlay of the deployed " + o})
	}
	return out
}
