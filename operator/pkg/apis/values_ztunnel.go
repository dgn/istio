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

type ZtunnelValues struct {
	// Internal defaults, should not be configured by users.
	InternalDefaultsDoNotSet map[string]any `json:"_internal_defaults_do_not_set,omitempty"`

	// K8s annotations for the ztunnel deployment.
	Annotations map[string]int `json:"annotations,omitempty"`

	// The address of the CA for CSR.
	CaAddress *string `json:"caAddress,omitempty"`

	// Specifies the compatibility version to use. When this is set, the control plane will
	// be configured with the same defaults as the specified version.
	CompatibilityVersion *string `json:"compatibilityVersion,omitempty"`

	// The name of the default revision in the cluster.
	DefaultRevision *string `json:"defaultRevision,omitempty"`

	// Custom DNS config for the ztunnel pods.
	//
	// See https://kubernetes.io/docs/concepts/services-networking/dns-pod-service/#dns-config
	DnsConfig *corev1.PodDNSConfig `json:"dnsConfig,omitempty"`

	// DNS policy for the ztunnel pods.
	DnsPolicy *string `json:"dnsPolicy,omitempty"`

	// Environment variables passed to the ztunnel container.
	Env map[string]string `json:"env,omitempty"`

	// Global configuration for Istio components.
	Global *GlobalValues `json:"global,omitempty"`

	// Specifies the docker hub for ztunnel images. Image will be `Hub/Image:Tag-Variant`.
	Hub *string `json:"hub,omitempty"`

	// Image name to pull from. Image will be `Hub/Image:Tag-Variant`.
	Image *string `json:"image,omitempty"`

	// Specifies the image pull policy for the ztunnel images. One of Always, Never, IfNotPresent.
	// Defaults to Always if :latest tag is specified, or IfNotPresent otherwise. Cannot be updated.
	//
	// More info: https://kubernetes.io/docs/concepts/containers/images#updating-images
	ImagePullPolicy *ImagePullPolicy `json:"imagePullPolicy,omitempty"`

	// ImagePullSecrets for the ztunnel ServiceAccount, list of secrets in the same namespace
	// to use for pulling any images in pods that reference this ServiceAccount.
	ImagePullSecrets []ZtunnelValuesImagePullSecretsElem `json:"imagePullSecrets,omitempty"`

	// Specifies the default namespace for the Istio control plane components.
	IstioNamespace *string `json:"istioNamespace,omitempty"`

	// Labels to add to the ztunnel pods.
	Labels map[string]string `json:"labels,omitempty"`

	// Specifies whether ztunnel should output logs in JSON format.
	LogAsJson *bool `json:"logAsJson,omitempty"`

	// Log level for ztunnel.
	LogLevel *string `json:"logLevel,omitempty"`

	// Defines runtime configuration of the mesh.
	//
	// See https://istio.io/docs/reference/config/istio.mesh.v1alpha1/
	MeshConfig *ZtunnelValuesMeshConfig `json:"meshConfig,omitempty"`

	// Specifies the Configuration for Istio mesh across multiple clusters through Istio gateways.
	MultiCluster *ZtunnelValuesMultiCluster `json:"multiCluster,omitempty"`

	// Network defines the network this cluster belongs to. This name
	// corresponds to the networks in the map of mesh networks.
	Network *string `json:"network,omitempty"`

	// Configuration for peer CA CRL.
	PeerCaCrl *ZtunnelValuesPeerCaCrl `json:"peerCaCrl,omitempty"`

	// Platform in which Istio is deployed. Possible values are: "openshift" and "gcp".
	// An empty value means it is a vanilla Kubernetes distribution, therefore no special
	// treatment will be considered.
	Platform *string `json:"platform,omitempty"`

	// K8s annotations for the ztunnel pods.
	//
	// See: https://kubernetes.io/docs/concepts/overview/working-with-objects/annotations/
	PodAnnotations *ZtunnelValuesPodAnnotations `json:"podAnnotations,omitempty"`

	// Labels to add to the ztunnel pods.
	PodLabels map[string]string `json:"podLabels,omitempty"`

	// Specifies which installation configuration profile to apply.
	Profile *string `json:"profile,omitempty"`

	// Name of the ztunnel resource.
	ResourceName *string `json:"resourceName,omitempty"`

	// The resource quotas configuration for the ztunnel DaemonSet.
	ResourceQuotas *ZtunnelValuesResourceQuotas `json:"resourceQuotas,omitempty"`

	// Specifies resource scope for discovery selectors.
	ResourceScope *ResourceScope `json:"resourceScope,omitempty"`

	// K8s resources settings for the ztunnel container.
	//
	// See https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container
	Resources *ZtunnelValuesResources `json:"resources,omitempty"`

	// Identifies the revision this installation is associated with.
	Revision *string `json:"revision,omitempty"`

	// SELinux options for the ztunnel pods.
	SeLinuxOptions *corev1.SELinuxOptions `json:"seLinuxOptions,omitempty"`

	// The container image tag to pull. Image will be `Hub/Image:Tag-Variant`.
	Tag any `json:"tag,omitempty"`

	// The duration in seconds before the ztunnel pod is terminated.
	TerminationGracePeriodSeconds *float64 `json:"terminationGracePeriodSeconds,omitempty"`

	// K8s tolerations for the ztunnel pods.
	//
	// See https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// K8s update strategy for the ztunnel DaemonSet.
	UpdateStrategy *ZtunnelValuesUpdateStrategy `json:"updateStrategy,omitempty"`

	// The container image variant to pull. Options are "debug" or "distroless".
	// Unset will use the default for the given version.
	Variant *string `json:"variant,omitempty"`

	// Additional volume mounts to add to the ztunnel container.
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// Additional volumes to add to the ztunnel pod.
	Volumes []corev1.Volume `json:"volumes,omitempty"`

	// The address of the XDS server.
	XdsAddress *string `json:"xdsAddress,omitempty"`
}

type ZtunnelValuesImagePullSecretsElem struct {
	// Secret name.
	Name *string `json:"name,omitempty"`
}

type ZtunnelValuesMeshConfig struct {
	// Default proxy configuration.
	DefaultConfig *ZtunnelValuesMeshConfigDefaultConfig `json:"defaultConfig,omitempty"`
}

type ZtunnelValuesMeshConfigDefaultConfig struct {
	// Proxy metadata configuration.
	ProxyMetadata map[string]any `json:"proxyMetadata,omitempty"`
}

// ZtunnelValuesMultiCluster specifies the Configuration for Istio mesh across multiple clusters.
type ZtunnelValuesMultiCluster struct {
	// The name of the cluster this installation will run in. This is required for
	// sidecar injection to properly label proxies.
	ClusterName *string `json:"clusterName,omitempty"`
}

// ZtunnelValuesPeerCaCrl is the configuration for peer CA CRL.
type ZtunnelValuesPeerCaCrl struct {
	// Controls whether peer CA CRL is enabled.
	Enabled *bool `json:"enabled,omitempty"`
}

type ZtunnelValuesPodAnnotations struct {
	// Port for Prometheus metrics scraping.
	PrometheusIoPort *string `json:"prometheus.io/port,omitempty"`

	// Controls whether Prometheus should scrape the ztunnel pods.
	PrometheusIoScrape *string `json:"prometheus.io/scrape,omitempty"`
}

// ZtunnelValuesResourceQuotas is the configuration for resource quotas for the ztunnel DaemonSet.
type ZtunnelValuesResourceQuotas struct {
	// Controls whether to create resource quotas or not for the ztunnel DaemonSet.
	Enabled *bool `json:"enabled,omitempty"`

	// The hard limit on the number of pods in the namespace where the ztunnel DaemonSet is deployed.
	Pods *int `json:"pods,omitempty"`
}

// ZtunnelValuesResources defines the compute resources for the ztunnel container.
type ZtunnelValuesResources struct {
	// The maximum amount of compute resources allowed.
	//
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/
	Limits *ZtunnelValuesResourcesLimits `json:"limits,omitempty"`

	// The minimum amount of compute resources required.
	//
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/
	Requests *ZtunnelValuesResourcesRequests `json:"requests,omitempty"`
}

type ZtunnelValuesResourcesLimits struct {
	// CPU resource limit.
	Cpu *string `json:"cpu,omitempty"`

	// Memory resource limit.
	Memory *string `json:"memory,omitempty"`
}

type ZtunnelValuesResourcesRequests struct {
	// CPU resource request.
	Cpu *string `json:"cpu,omitempty"`

	// Memory resource request.
	Memory *string `json:"memory,omitempty"`
}

// ZtunnelValuesUpdateStrategy is the update strategy for the ztunnel DaemonSet.
type ZtunnelValuesUpdateStrategy struct {
	// Rolling update configuration.
	RollingUpdate *ZtunnelValuesUpdateStrategyRollingUpdate `json:"rollingUpdate,omitempty"`

	// Type of update strategy. Can be "RollingUpdate" or "OnDelete".
	Type *string `json:"type,omitempty"`
}

// ZtunnelValuesUpdateStrategyRollingUpdate is the rolling update configuration.
type ZtunnelValuesUpdateStrategyRollingUpdate struct {
	// The maximum number of pods that can be scheduled above the desired number of pods during an update.
	MaxSurge *IntOrString `json:"maxSurge,omitempty"`

	// The maximum number of pods that can be unavailable during a rolling update.
	MaxUnavailable *IntOrString `json:"maxUnavailable,omitempty"`
}
