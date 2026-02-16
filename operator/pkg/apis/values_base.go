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

type BaseValues struct {
	// Internal defaults, should not be configured by users.
	InternalDefaultsDoNotSet map[string]any `json:"_internal_defaults_do_not_set,omitempty"`

	// Configuration for the base component.
	Base *BaseValuesBase `json:"base,omitempty"`

	// Specifies the compatibility version to use. When this is set, the control plane will
	// be configured with the same defaults as the specified version.
	CompatibilityVersion *string `json:"compatibilityVersion,omitempty"`

	// The name of the default revision in the cluster.
	DefaultRevision *string `json:"defaultRevision,omitempty"`

	// Field used as a condition when this chart is included as a dependency. It's
	// allowed in the schema, but the chart itself does not read it. For more
	// information see:
	// https://helm.sh/docs/chart_best_practices/dependencies/#conditions-and-tags.
	Enabled *bool `json:"enabled,omitempty"`

	// Specifies experimental helm fields that could be removed or changed in the future.
	Experimental *BaseValuesExperimental `json:"experimental,omitempty"`

	// Global configuration for Istio components.
	Global *GlobalValues `json:"global,omitempty"`

	// Platform in which Istio is deployed. Possible values are: "openshift" and "gcp".
	// An empty value means it is a vanilla Kubernetes distribution, therefore no special
	// treatment will be considered.
	Platform *string `json:"platform,omitempty"`

	// Specifies which installation configuration profile to apply.
	Profile *string `json:"profile,omitempty"`

	// Identifies the revision this installation is associated with.
	Revision *string `json:"revision,omitempty"`
}

// BaseValuesBase is the configuration for the base chart.
type BaseValuesBase struct {
	// For Helm2 use, adds the CRDs to templates.
	EnableCRDTemplates *bool `json:"enableCRDTemplates,omitempty"`

	// For istioctl usage to disable istio config crds in base.
	EnableIstioConfigCRDs *bool `json:"enableIstioConfigCRDs,omitempty"`

	// CRDs to exclude. Requires `enableCRDTemplates`.
	ExcludedCRDs []string `json:"excludedCRDs,omitempty"`

	// Validation webhook CA bundle.
	ValidationCABundle *string `json:"validationCABundle,omitempty"`

	// URL to use for validating webhook.
	ValidationURL *string `json:"validationURL,omitempty"`
}

// BaseValuesExperimental is a placeholder for experimental installation features.
type BaseValuesExperimental struct {
	// Controls whether the experimental stable validation policy feature is enabled.
	StableValidationPolicy *bool `json:"stableValidationPolicy,omitempty"`
}
