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

import (
	corev1 "k8s.io/api/core/v1"
)

type IngressValues struct {
	// Internal defaults, not to be set by users.
	InternalDefaultsDoNotSet map[string]any `json:"_internal_defaults_do_not_set,omitempty"`

	// Specifies the compatibility version to use. When this is set, the control plane will
	// be configured with the same defaults as the specified version.
	CompatibilityVersion *string `json:"compatibilityVersion,omitempty"`

	// The name of the default revision in the cluster.
	DefaultRevision *string `json:"defaultRevision,omitempty"`

	// Configuration for ingress and egress gateways.
	Gateways *IngressValuesGateways `json:"gateways,omitempty"`

	// Global configuration for Istio components.
	Global *GlobalValues `json:"global,omitempty"`

	// Defines runtime configuration of components, including Istiod and istio-agent behavior.
	// See https://istio.io/docs/reference/config/istio.mesh.v1alpha1/ for all available options.
	MeshConfig *IngressValuesMeshConfig `json:"meshConfig,omitempty"`

	// Used internally to identify the owner of each resource.
	OwnerName *string `json:"ownerName,omitempty"`

	// Platform in which Istio is deployed. Possible values are: "openshift" and "gcp".
	// An empty value means it is a vanilla Kubernetes distribution, therefore no special
	// treatment will be considered.
	Platform *string `json:"platform,omitempty"`

	// Specifies which installation configuration profile to apply.
	Profile *string `json:"profile,omitempty"`

	// Identifies the revision this installation is associated with.
	Revision *string `json:"revision,omitempty"`
}

// IngressValuesGateways is the configuration for gateways.
type IngressValuesGateways struct {
	// Configuration for an ingress gateway.
	IstioIngressgateway *IngressValuesGatewaysIstioIngressgateway `json:"istio-ingressgateway,omitempty"`
}

// IngressValuesGatewaysIstioIngressgateway is the configuration for an ingress gateway.
type IngressValuesGatewaysIstioIngressgateway struct {
	AdditionalContainers []corev1.Container `json:"additionalContainers,omitempty"`

	// Controls whether auto scaling with a HorizontalPodAutoscaler is enabled.
	AutoscaleEnabled *bool `json:"autoscaleEnabled,omitempty"`

	// maxReplicas setting for HorizontalPodAutoscaler.
	AutoscaleMax *int `json:"autoscaleMax,omitempty"`

	// minReplicas setting for HorizontalPodAutoscaler.
	AutoscaleMin *int `json:"autoscaleMin,omitempty"`

	ConfigVolumes []corev1.Volume `json:"configVolumes,omitempty"`

	// K8s cpu utilization setting for HorizontalPodAutoscaler target.
	//
	// Deprecated: See https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/
	Cpu *IngressValuesGatewaysIstioIngressgatewayCpu `json:"cpu,omitempty"`

	CustomService *bool `json:"customService,omitempty"`

	// Environment variables passed to the proxy container.
	Env map[string]string `json:"env,omitempty"`

	ExternalTrafficPolicy *string `json:"externalTrafficPolicy,omitempty"`

	IngressPorts []map[string]any `json:"ingressPorts,omitempty"`

	// The injection template to use for the gateway. If not set, no injection will be performed.
	InjectionTemplate *string `json:"injectionTemplate,omitempty"`

	// Defines which IP family to use for single stack or the order of IP families for dual-stack.
	// Valid list items are "IPv4", "IPv6".
	//
	// More info: https://kubernetes.io/docs/concepts/services-networking/dual-stack/#services
	IpFamilies []IngressValuesGatewaysIstioIngressgatewayIpFamiliesElem `json:"ipFamilies,omitempty"`

	// Controls whether Services are configured to use IPv4, IPv6, or both. Valid options
	// are PreferDualStack, RequireDualStack, and SingleStack.
	//
	// More info: https://kubernetes.io/docs/concepts/services-networking/dual-stack/#services
	IpFamilyPolicy *IngressValuesGatewaysIstioIngressgatewayIpFamilyPolicy `json:"ipFamilyPolicy,omitempty"`

	Labels map[string]string `json:"labels,omitempty"`

	LoadBalancerIP *string `json:"loadBalancerIP,omitempty"`

	LoadBalancerSourceRanges []string `json:"loadBalancerSourceRanges,omitempty"`

	// K8s memory utilization setting for HorizontalPodAutoscaler target.
	//
	// Deprecated: See https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/
	Memory *IngressValuesGatewaysIstioIngressgatewayMemory `json:"memory,omitempty"`

	Name *string `json:"name,omitempty"`

	// K8s node selector.
	//
	// See https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#nodeselector
	//
	// Deprecated: use pod-level node selection.
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// K8s annotations for pods.
	//
	// See: https://kubernetes.io/docs/concepts/overview/working-with-objects/annotations/
	//
	// Deprecated: use pod-level annotations.
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`

	// Pod anti-affinity label selector.
	//
	// Deprecated: use pod-level affinity configuration.
	PodAntiAffinityLabelSelector []map[string]any `json:"podAntiAffinityLabelSelector,omitempty"`

	// See PodAntiAffinityLabelSelector.
	//
	// Deprecated: use pod-level affinity configuration.
	PodAntiAffinityTermLabelSelector []map[string]any `json:"podAntiAffinityTermLabelSelector,omitempty"`

	// Port configuration for the ingress gateway.
	Ports []IngressValuesGatewaysIstioIngressgatewayPortsElem `json:"ports,omitempty"`

	// K8s resources settings.
	//
	// See https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container
	//
	// Deprecated: use pod-level resource configuration.
	Resources *IngressValuesGatewaysIstioIngressgatewayResources `json:"resources,omitempty"`

	// K8s rolling update strategy.
	//
	// Deprecated: use deployment-level strategy configuration.
	RollingMaxSurge *IntOrString `json:"rollingMaxSurge,omitempty"`

	// The number of pods that can be unavailable during a rolling update (see
	// `strategy.rollingUpdate.maxUnavailable` here:
	// https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/deployment-v1/#DeploymentSpec).
	// May be specified as a number of pods or as a percent of the total number
	// of pods at the start of the update.
	//
	// Deprecated: use deployment-level strategy configuration.
	RollingMaxUnavailable *IntOrString `json:"rollingMaxUnavailable,omitempty"`

	RunAsRoot *bool `json:"runAsRoot,omitempty"`

	// Config for secret volume mounts.
	SecretVolumes []IngressValuesGatewaysIstioIngressgatewaySecretVolumesElem `json:"secretVolumes,omitempty"`

	ServiceAccount *IngressValuesGatewaysIstioIngressgatewayServiceAccount `json:"serviceAccount,omitempty"`

	// Annotations to add to the ingress gateway service.
	ServiceAnnotations map[string]string `json:"serviceAnnotations,omitempty"`

	// Deprecated: use pod-level toleration configuration.
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Service type.
	//
	// See https://kubernetes.io/docs/concepts/services-networking/service/#publishing-services-service-types
	Type *string `json:"type,omitempty"`
}

// IngressValuesGatewaysIstioIngressgatewayCpu is the configuration for CPU target utilization
// for HorizontalPodAutoscaler target.
type IngressValuesGatewaysIstioIngressgatewayCpu struct {
	// K8s utilization setting for HorizontalPodAutoscaler target.
	//
	// See https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/
	TargetAverageUtilization *int `json:"targetAverageUtilization,omitempty"`
}

type IngressValuesGatewaysIstioIngressgatewayIpFamiliesElem string

const IngressValuesGatewaysIstioIngressgatewayIpFamiliesElemIPv4 IngressValuesGatewaysIstioIngressgatewayIpFamiliesElem = "IPv4"
const IngressValuesGatewaysIstioIngressgatewayIpFamiliesElemIPv6 IngressValuesGatewaysIstioIngressgatewayIpFamiliesElem = "IPv6"

type IngressValuesGatewaysIstioIngressgatewayIpFamilyPolicy string

const IngressValuesGatewaysIstioIngressgatewayIpFamilyPolicyBlank IngressValuesGatewaysIstioIngressgatewayIpFamilyPolicy = ""
const IngressValuesGatewaysIstioIngressgatewayIpFamilyPolicyPreferDualStack IngressValuesGatewaysIstioIngressgatewayIpFamilyPolicy = "PreferDualStack"
const IngressValuesGatewaysIstioIngressgatewayIpFamilyPolicyRequireDualStack IngressValuesGatewaysIstioIngressgatewayIpFamilyPolicy = "RequireDualStack"
const IngressValuesGatewaysIstioIngressgatewayIpFamilyPolicySingleStack IngressValuesGatewaysIstioIngressgatewayIpFamilyPolicy = "SingleStack"

// IngressValuesGatewaysIstioIngressgatewayMemory is the configuration for memory target utilization
// for HorizontalPodAutoscaler target.
type IngressValuesGatewaysIstioIngressgatewayMemory struct {
	// K8s utilization setting for HorizontalPodAutoscaler target.
	//
	// See https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/
	TargetAverageUtilization *int `json:"targetAverageUtilization,omitempty"`
}

// IngressValuesGatewaysIstioIngressgatewayPortsElem is the configuration for a port.
type IngressValuesGatewaysIstioIngressgatewayPortsElem struct {
	// Port name.
	Name *string `json:"name,omitempty"`

	// Port number.
	Port *int `json:"port,omitempty"`

	// Protocol name.
	Protocol *string `json:"protocol,omitempty"`

	// Target port number.
	TargetPort *int `json:"targetPort,omitempty"`
}

// IngressValuesGatewaysIstioIngressgatewayResources defines compute resources required by a container.
type IngressValuesGatewaysIstioIngressgatewayResources struct {
	// The maximum amount of compute resources allowed.
	//
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/
	Limits *IngressValuesGatewaysIstioIngressgatewayResourcesLimits `json:"limits,omitempty"`

	// The minimum amount of compute resources required. If Requests is omitted for a container,
	// it defaults to Limits if that is explicitly specified, otherwise to an implementation-defined value.
	//
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/
	Requests *IngressValuesGatewaysIstioIngressgatewayResourcesRequests `json:"requests,omitempty"`
}

type IngressValuesGatewaysIstioIngressgatewayResourcesLimits struct {
	// CPU limit.
	Cpu *string `json:"cpu,omitempty"`

	// Memory limit.
	Memory *string `json:"memory,omitempty"`
}

type IngressValuesGatewaysIstioIngressgatewayResourcesRequests struct {
	// CPU requests.
	Cpu *string `json:"cpu,omitempty"`

	// Memory requests.
	Memory *string `json:"memory,omitempty"`
}

// IngressValuesGatewaysIstioIngressgatewaySecretVolumesElem is the configuration for secret volume mounts.
//
// See https://kubernetes.io/docs/concepts/configuration/secret/#using-secrets.
type IngressValuesGatewaysIstioIngressgatewaySecretVolumesElem struct {
	MountPath *string `json:"mountPath,omitempty"`

	Name *string `json:"name,omitempty"`

	SecretName *string `json:"secretName,omitempty"`
}

// IngressValuesGatewaysIstioIngressgatewayServiceAccount mirrors the Kubernetes ServiceAccount for unmarshaling.
type IngressValuesGatewaysIstioIngressgatewayServiceAccount struct {
	Annotations map[string]string `json:"annotations,omitempty"`
}

type IngressValuesMeshConfig struct {
	// Default proxy configuration.
	DefaultConfig *IngressValuesMeshConfigDefaultConfig `json:"defaultConfig,omitempty"`

	// Enables Prometheus metrics merging.
	EnablePrometheusMerge *bool `json:"enablePrometheusMerge,omitempty"`

	// The trust domain corresponds to the trust root of a system.
	TrustDomain *string `json:"trustDomain,omitempty"`
}

type IngressValuesMeshConfigDefaultConfig struct {
	// Additional metadata to include in proxied requests.
	ProxyMetadata map[string]any `json:"proxyMetadata,omitempty"`
}
