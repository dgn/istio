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

type GatewayValues struct {
	// Internal defaults, should not be configured by users.
	InternalDefaultsDoNotSet map[string]any `json:"_internal_defaults_do_not_set,omitempty"`

	// Additional containers to add to the gateway pod.
	AdditionalContainers []corev1.Container `json:"additionalContainers,omitempty"`

	// K8s affinity to set on the gateway pods.
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// K8s annotations for the gateway deployment.
	Annotations map[string]int `json:"annotations,omitempty"`

	// Configuration for auto scaling with a HorizontalPodAutoscaler.
	Autoscaling *GatewayValuesAutoscaling `json:"autoscaling,omitempty"`

	// Specifies the compatibility version to use. When this is set, the control plane will
	// be configured with the same defaults as the specified version.
	CompatibilityVersion *string `json:"compatibilityVersion,omitempty"`

	// The container-level security context for the gateway container.
	ContainerSecurityContext *corev1.SecurityContext `json:"containerSecurityContext,omitempty"`

	// The name of the default revision in the cluster.
	DefaultRevision *string `json:"defaultRevision,omitempty"`

	// Field used as a condition when this chart is included as a dependency. It's
	// allowed in the schema, but the chart itself does not read it. For more
	// information see:
	// https://helm.sh/docs/chart_best_practices/dependencies/#conditions-and-tags.
	Enabled *bool `json:"enabled,omitempty"`

	// Environment variables passed to the gateway container.
	Env map[string]string `json:"env,omitempty"`

	// Additional environment variables sourced from ConfigMaps or Secrets.
	EnvVarFrom []GatewayValuesEnvVarFromElem `json:"envVarFrom,omitempty"`

	// Global configuration for Istio components.
	Global *GlobalValues `json:"global,omitempty"`

	// Specifies the image pull policy for the gateway images. One of Always, Never, IfNotPresent.
	// Defaults to Always if :latest tag is specified, or IfNotPresent otherwise. Cannot be updated.
	//
	// More info: https://kubernetes.io/docs/concepts/containers/images#updating-images
	ImagePullPolicy *ImagePullPolicy `json:"imagePullPolicy,omitempty"`

	// ImagePullSecrets for the gateway ServiceAccount, list of secrets in the same namespace
	// to use for pulling any images in pods that reference this ServiceAccount.
	ImagePullSecrets []GatewayValuesImagePullSecretsElem `json:"imagePullSecrets,omitempty"`

	// Init containers to add to the gateway pod.
	InitContainers []corev1.Container `json:"initContainers,omitempty"`

	// The workload kind to use for the gateway. Can be "Deployment" or "DaemonSet".
	Kind *GatewayValuesKind `json:"kind,omitempty"`

	// Labels to add to the gateway pods.
	Labels map[string]string `json:"labels,omitempty"`

	// The k8s lifecycle hooks definition for the gateway container.
	//
	// More info: https://kubernetes.io/docs/concepts/containers/container-lifecycle-hooks/#container-hooks
	Lifecycle *GatewayValuesLifecycle `json:"lifecycle,omitempty"`

	// The minimum number of seconds for which a newly created pod should be ready
	// without any of its containers crashing, for it to be considered available.
	MinReadySeconds *int `json:"minReadySeconds,omitempty"`

	// Name of the gateway deployment.
	Name *string `json:"name,omitempty"`

	// The network gateway for cross-network traffic.
	NetworkGateway *string `json:"networkGateway,omitempty"`

	// K8s node selector for the gateway pods.
	//
	// See https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#nodeselector
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Configuration for Pilot passed through to the gateway.
	Pilot map[string]any `json:"pilot,omitempty"`

	// Platform in which Istio is deployed. Possible values are: "openshift" and "gcp".
	// An empty value means it is a vanilla Kubernetes distribution, therefore no special
	// treatment will be considered.
	Platform *string `json:"platform,omitempty"`

	// K8s annotations for the gateway pods.
	//
	// See: https://kubernetes.io/docs/concepts/overview/working-with-objects/annotations/
	PodAnnotations *GatewayValuesPodAnnotations `json:"podAnnotations,omitempty"`

	// K8s PodDisruptionBudget configuration for the gateway.
	//
	// See https://kubernetes.io/docs/concepts/workloads/pods/disruptions/
	PodDisruptionBudget *GatewayValuesPodDisruptionBudget `json:"podDisruptionBudget,omitempty"`

	// Specifies the k8s priorityClassName for the gateway pods.
	//
	// See https://kubernetes.io/docs/concepts/configuration/pod-priority-preemption/#priorityclass
	PriorityClassName *string `json:"priorityClassName,omitempty"`

	// Specifies which installation configuration profile to apply.
	Profile *string `json:"profile,omitempty"`

	// Configuration for RBAC resources.
	Rbac *GatewayValuesRbac `json:"rbac,omitempty"`

	// K8s readiness probe configuration for the gateway container.
	ReadinessProbe *corev1.Probe `json:"readinessProbe,omitempty"`

	// Number of replicas for the gateway deployment.
	ReplicaCount *int `json:"replicaCount,omitempty"`

	// K8s resources settings for the gateway container.
	//
	// See https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container
	Resources *GatewayValuesResources `json:"resources,omitempty"`

	// Identifies the revision this installation is associated with.
	Revision *string `json:"revision,omitempty"`

	// Controls whether the gateway should run as root.
	RunAsRoot *bool `json:"runAsRoot,omitempty"`

	// The pod-level security context for the gateway pods.
	SecurityContext *corev1.PodSecurityContext `json:"securityContext,omitempty"`

	// Configuration for the gateway Kubernetes Service.
	Service *GatewayValuesService `json:"service,omitempty"`

	// Configuration for the gateway ServiceAccount.
	ServiceAccount *GatewayValuesServiceAccount `json:"serviceAccount,omitempty"`

	// K8s deployment strategy for the gateway.
	Strategy map[string]any `json:"strategy,omitempty"`

	// The duration in seconds before the gateway pod is terminated.
	TerminationGracePeriodSeconds *float64 `json:"terminationGracePeriodSeconds,omitempty"`

	// K8s tolerations for the gateway pods.
	//
	// See https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// K8s topology spread constraints for the gateway pods.
	//
	// See https://kubernetes.io/docs/concepts/workloads/pods/pod-topology-spread-constraints/
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`

	// Controls whether the gateway should use an unprivileged port.
	UnprivilegedPort *GatewayValuesUnprivilegedPort `json:"unprivilegedPort,omitempty"`

	// Additional volume mounts to add to the gateway container.
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// Additional volumes to add to the gateway pod.
	Volumes []corev1.Volume `json:"volumes,omitempty"`
}

type GatewayValuesAutoscaling struct {
	// Controls whether auto scaling with a HorizontalPodAutoscaler is enabled.
	Enabled *bool `json:"enabled,omitempty"`

	// maxReplicas setting for HorizontalPodAutoscaler.
	MaxReplicas *int `json:"maxReplicas,omitempty"`

	// minReplicas setting for HorizontalPodAutoscaler.
	MinReplicas *int `json:"minReplicas,omitempty"`

	// Target CPU utilization percentage for HorizontalPodAutoscaler.
	TargetCPUUtilizationPercentage *int `json:"targetCPUUtilizationPercentage,omitempty"`
}

type GatewayValuesEnvVarFromElem struct {
	// Environment variable name.
	Name *string `json:"name,omitempty"`

	// Source for the environment variable's value.
	ValueFrom map[string]any `json:"valueFrom,omitempty"`
}

type GatewayValuesImagePullSecretsElem struct {
	// Secret name.
	Name *string `json:"name,omitempty"`
}

type GatewayValuesKind string

const GatewayValuesKindDaemonSet GatewayValuesKind = "DaemonSet"
const GatewayValuesKindDeployment GatewayValuesKind = "Deployment"

type GatewayValuesLifecycle struct {
	// PostStart lifecycle handler.
	PostStart map[string]any `json:"postStart,omitempty"`

	// PreStop lifecycle handler.
	PreStop map[string]any `json:"preStop,omitempty"`
}

type GatewayValuesPodAnnotations struct {
	// The injection template to use for the gateway pods.
	InjectIstioIoTemplates *string `json:"inject.istio.io/templates,omitempty"`

	// Path for Prometheus metrics scraping.
	PrometheusIoPath *string `json:"prometheus.io/path,omitempty"`

	// Port for Prometheus metrics scraping.
	PrometheusIoPort *string `json:"prometheus.io/port,omitempty"`

	// Controls whether Prometheus should scrape the gateway pods.
	PrometheusIoScrape *string `json:"prometheus.io/scrape,omitempty"`
}

type GatewayValuesPodDisruptionBudget struct {
	// Maximum number of pods that can be unavailable during disruption.
	MaxUnavailable *IntOrString `json:"maxUnavailable,omitempty"`

	// Minimum number of pods that must be available during disruption.
	MinAvailable *IntOrString `json:"minAvailable,omitempty"`

	// Policy for evicting unhealthy pods. Can be "AlwaysAllow" or "IfHealthyBudget".
	UnhealthyPodEvictionPolicy *GatewayValuesPodDisruptionBudgetUnhealthyPodEvictionPolicy `json:"unhealthyPodEvictionPolicy,omitempty"`
}

type GatewayValuesPodDisruptionBudgetUnhealthyPodEvictionPolicy string

const GatewayValuesPodDisruptionBudgetUnhealthyPodEvictionPolicyAlwaysAllow GatewayValuesPodDisruptionBudgetUnhealthyPodEvictionPolicy = "AlwaysAllow"
const GatewayValuesPodDisruptionBudgetUnhealthyPodEvictionPolicyBlank GatewayValuesPodDisruptionBudgetUnhealthyPodEvictionPolicy = ""
const GatewayValuesPodDisruptionBudgetUnhealthyPodEvictionPolicyIfHealthyBudget GatewayValuesPodDisruptionBudgetUnhealthyPodEvictionPolicy = "IfHealthyBudget"

type GatewayValuesRbac struct {
	// Controls whether RBAC resources are created.
	Enabled *bool `json:"enabled,omitempty"`
}

// GatewayValuesResources defines the compute resources for the gateway container.
type GatewayValuesResources struct {
	// The maximum amount of compute resources allowed.
	//
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/
	Limits *GatewayValuesResourcesLimits `json:"limits,omitempty"`

	// The minimum amount of compute resources required. If Requests is omitted for a container,
	// it defaults to Limits if that is explicitly specified, otherwise to an implementation-defined value.
	//
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/
	Requests *GatewayValuesResourcesRequests `json:"requests,omitempty"`
}

type GatewayValuesResourcesLimits struct {
	// CPU resource limit.
	Cpu *string `json:"cpu,omitempty"`

	// Memory resource limit.
	Memory *string `json:"memory,omitempty"`
}

type GatewayValuesResourcesRequests struct {
	// CPU resource request.
	Cpu *string `json:"cpu,omitempty"`

	// Memory resource request.
	Memory *string `json:"memory,omitempty"`
}

// GatewayValuesService is the configuration for the gateway Kubernetes Service.
type GatewayValuesService struct {
	// Annotations to add to the gateway service.
	Annotations map[string]string `json:"annotations,omitempty"`

	// Controls the external traffic policy for the service.
	ExternalTrafficPolicy *string `json:"externalTrafficPolicy,omitempty"`

	// Defines which IP family to use for single stack or the order of IP families for dual-stack.
	// Valid list items are "IPv4", "IPv6".
	//
	// More info: https://kubernetes.io/docs/concepts/services-networking/dual-stack/#services
	IpFamilies []string `json:"ipFamilies,omitempty"`

	// Controls whether Services are configured to use IPv4, IPv6, or both. Valid options
	// are PreferDualStack, RequireDualStack, and SingleStack.
	//
	// More info: https://kubernetes.io/docs/concepts/services-networking/dual-stack/#services
	IpFamilyPolicy *GatewayValuesServiceIpFamilyPolicy `json:"ipFamilyPolicy,omitempty"`

	// The load balancer IP address for the gateway service.
	LoadBalancerIP *string `json:"loadBalancerIP,omitempty"`

	// The source ranges allowed to access the load balancer.
	LoadBalancerSourceRanges []string `json:"loadBalancerSourceRanges,omitempty"`

	// Port configuration for the gateway service.
	Ports []GatewayValuesServicePortsElem `json:"ports,omitempty"`

	// Additional selector labels for the gateway service.
	SelectorLabels map[string]string `json:"selectorLabels,omitempty"`

	// Service type.
	//
	// See https://kubernetes.io/docs/concepts/services-networking/service/#publishing-services-service-types
	Type *string `json:"type,omitempty"`
}

// GatewayValuesServiceAccount is the configuration for the gateway ServiceAccount.
type GatewayValuesServiceAccount struct {
	// Annotations to add to the gateway service account.
	Annotations map[string]string `json:"annotations,omitempty"`

	// Controls whether a ServiceAccount is created.
	Create *bool `json:"create,omitempty"`

	// Name of the ServiceAccount to use.
	Name *string `json:"name,omitempty"`
}

type GatewayValuesServiceIpFamilyPolicy string

const GatewayValuesServiceIpFamilyPolicyBlank GatewayValuesServiceIpFamilyPolicy = ""
const GatewayValuesServiceIpFamilyPolicyPreferDualStack GatewayValuesServiceIpFamilyPolicy = "PreferDualStack"
const GatewayValuesServiceIpFamilyPolicyRequireDualStack GatewayValuesServiceIpFamilyPolicy = "RequireDualStack"
const GatewayValuesServiceIpFamilyPolicySingleStack GatewayValuesServiceIpFamilyPolicy = "SingleStack"

// GatewayValuesServicePortsElem is the configuration for a service port.
type GatewayValuesServicePortsElem struct {
	// Port name.
	Name *string `json:"name,omitempty"`

	// Port number.
	Port *int `json:"port,omitempty"`

	// Protocol name.
	Protocol *string `json:"protocol,omitempty"`

	// Target port number.
	TargetPort *int `json:"targetPort,omitempty"`
}

type GatewayValuesUnprivilegedPort struct {
	Value any
}
