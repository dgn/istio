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

package apis

type DefaultchartValues struct {
	// Internal defaults, should not be configured by users.
	InternalDefaultsDoNotSet map[string]any `json:"_internal_defaults_do_not_set,omitempty"`

	// Configuration for the base component.
	Base *DefaultchartValuesBase `json:"base,omitempty"`

	// Specifies the compatibility version to use. When this is set, the control plane will
	// be configured with the same defaults as the specified version.
	CompatibilityVersion *string `json:"compatibilityVersion,omitempty"`

	// The name of the default revision in the cluster.
	DefaultRevision *string `json:"defaultRevision,omitempty"`

	// Global configuration for Istio components.
	Global *GlobalValues `json:"global,omitempty"`

	// Configuration for istiod-remote.
	//
	// Deprecated: istiod-remote chart is removed and replaced with
	// `istio-discovery --set values.istiodRemote.enabled=true`
	IstiodRemote *DefaultchartValuesIstiodRemote `json:"istiodRemote,omitempty"`

	// Platform in which Istio is deployed. Possible values are: "openshift" and "gcp".
	// An empty value means it is a vanilla Kubernetes distribution, therefore no special
	// treatment will be considered.
	Platform *string `json:"platform,omitempty"`

	// Specifies which installation configuration profile to apply.
	Profile *string `json:"profile,omitempty"`

	// Identifies the revision this installation is associated with.
	Revision *string `json:"revision,omitempty"`

	// Configuration for the sidecar injector webhook.
	SidecarInjectorWebhook *DefaultchartValuesSidecarInjectorWebhook `json:"sidecarInjectorWebhook,omitempty"`
}

// DefaultchartValuesBase is the configuration for the base chart.
type DefaultchartValuesBase struct {
	// URL to use for validating webhook.
	ValidationURL *string `json:"validationURL,omitempty"`
}

// DefaultchartValuesIstiodRemote is the configuration for istiod-remote.
type DefaultchartValuesIstiodRemote struct {
	// URL to use for sidecar injector webhook.
	InjectionURL *string `json:"injectionURL,omitempty"`
}

// DefaultchartValuesSidecarInjectorWebhook is the configuration for the sidecar injector webhook.
type DefaultchartValuesSidecarInjectorWebhook struct {
	// Enables sidecar auto-injection in namespaces by default.
	EnableNamespacesByDefault *bool `json:"enableNamespacesByDefault,omitempty"`
}
