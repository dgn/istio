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

// DiscoveryValues is the configuration for the Pilot component (istio-discovery chart).
type DiscoveryValues struct {
	// Internal defaults - should not be set by users.
	InternalDefaultsDoNotSet map[string]any `json:"_internal_defaults_do_not_set,omitempty"`

	// K8s affinity to set on the Pilot Pods.
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// See https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/#configurable-scaling-behavior
	AutoscaleBehavior map[string]any `json:"autoscaleBehavior,omitempty"`

	// Controls whether a HorizontalPodAutoscaler is installed for Pilot.
	AutoscaleEnabled *bool `json:"autoscaleEnabled,omitempty"`

	// Maximum number of replicas in the HorizontalPodAutoscaler for Pilot.
	AutoscaleMax *int `json:"autoscaleMax,omitempty"`

	// Minimum number of replicas in the HorizontalPodAutoscaler for Pilot.
	AutoscaleMin *int `json:"autoscaleMin,omitempty"`

	// Configuration for the base component.
	Base *DiscoveryValuesBase `json:"base,omitempty"`

	// Configures whether to use an existing CNI installation for workloads.
	Cni *DiscoveryValuesCni `json:"cni,omitempty"`

	// Specifies the compatibility version to use. When this is set, the control plane will
	// be configured with the same defaults as the specified version.
	CompatibilityVersion *string `json:"compatibilityVersion,omitempty"`

	// Configuration settings passed to Pilot as a ConfigMap.
	//
	// This controls whether the mesh config map, generated from values.yaml is generated.
	// If false, pilot will use default values or user-supplied values, in that order of preference.
	ConfigMap *bool `json:"configMap,omitempty"`

	// Target CPU utilization used in HorizontalPodAutoscaler.
	//
	// See https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/
	//
	// Deprecated: use autoscaleBehavior.
	Cpu *DiscoveryValuesCpu `json:"cpu,omitempty"`

	// The name of the default revision in the cluster.
	DefaultRevision *string `json:"defaultRevision,omitempty"`

	// K8s annotations for the Pilot deployment.
	DeploymentAnnotations map[string]string `json:"deploymentAnnotations,omitempty"`

	// Labels that are added to Pilot deployment.
	//
	// See https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/
	DeploymentLabels map[string]string `json:"deploymentLabels,omitempty"`

	// Environment variables passed to the Pilot container.
	//
	// Examples:
	//
	//	env:
	//	  ENV_VAR_1: value1
	//	  ENV_VAR_2: value2
	Env map[string]string `json:"env,omitempty"`

	// Additional environment variables sourced from ConfigMaps/Secrets for the Pilot container.
	EnvVarFrom []DiscoveryValuesEnvVarFromElem `json:"envVarFrom,omitempty"`

	// Specifies experimental helm fields that could be removed or changed in the future.
	Experimental *ValuesExperimental `json:"experimental,omitempty"`

	// Additional container arguments for the Pilot container.
	ExtraContainerArgs []string `json:"extraContainerArgs,omitempty"`

	// Configuration for Gateway Classes.
	GatewayClasses map[string]any `json:"gatewayClasses,omitempty"`

	// Configuration for ingress and egress gateways.
	Gateways *DiscoveryValuesGateways `json:"gateways,omitempty"`

	// Global configuration for Istio components.
	Global *GlobalValues `json:"global,omitempty"`

	// Hub to pull the container image from. Image will be `Hub/Image:Tag-Variant`.
	Hub *string `json:"hub,omitempty"`

	// Image name used for Pilot.
	//
	// This can be set either to image name if hub is also set, or can be set to the full hub:name string.
	//
	// Examples: custom-pilot, docker.io/someuser:custom-pilot
	Image *string `json:"image,omitempty"`

	// Additional init containers for the Pilot pod.
	InitContainers []corev1.Container `json:"initContainers,omitempty"`

	// Defines which IP family to use for single stack or the order of IP families for dual-stack.
	// Valid list items are "IPv4", "IPv6".
	//
	// More info: https://kubernetes.io/docs/concepts/services-networking/dual-stack/#services
	IpFamilies []string `json:"ipFamilies,omitempty"`

	// Controls whether Services are configured to use IPv4, IPv6, or both. Valid options
	// are PreferDualStack, RequireDualStack, and SingleStack.
	//
	// More info: https://kubernetes.io/docs/concepts/services-networking/dual-stack/#services
	IpFamilyPolicy *string `json:"ipFamilyPolicy,omitempty"`

	// Configuration for istiod-remote.
	IstiodRemote *DiscoveryValuesIstiodRemote `json:"istiodRemote,omitempty"`

	// Specifies an extra root certificate in PEM format. This certificate will be trusted
	// by pilot when resolving JWKS URIs.
	JwksResolverExtraRootCA *string `json:"jwksResolverExtraRootCA,omitempty"`

	// Maximum duration that a sidecar can be connected to a pilot.
	//
	// This setting balances out load across pilot instances, but adds some resource overhead.
	//
	// Examples: 300s, 30m, 1h
	KeepaliveMaxServerConnectionAge *string `json:"keepaliveMaxServerConnectionAge,omitempty"`

	// Target memory utilization used in HorizontalPodAutoscaler.
	//
	// See https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/
	//
	// Deprecated: use autoscaleBehavior.
	Memory *DiscoveryValuesMemory `json:"memory,omitempty"`

	// Defines runtime configuration of components, including Istiod and istio-agent behavior.
	// See https://istio.io/docs/reference/config/istio.mesh.v1alpha1/ for all available options.
	MeshConfig *DiscoveryValuesMeshConfig `json:"meshConfig,omitempty"`

	// K8s node selector.
	//
	// See https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#nodeselector
	//
	// Deprecated: use pod-level node selection.
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Used internally to identify the owner of each resource.
	OwnerName *string `json:"ownerName,omitempty"`

	// Pod disruption budget configuration for Pilot.
	Pdb *DiscoveryValuesPdb `json:"pdb,omitempty"`

	// Platform in which Istio is deployed. Possible values are: "openshift" and "gcp".
	// An empty value means it is a vanilla Kubernetes distribution, therefore no special
	// treatment will be considered.
	Platform *string `json:"platform,omitempty"`

	// K8s annotations for pods.
	//
	// See: https://kubernetes.io/docs/concepts/overview/working-with-objects/annotations/
	//
	// Deprecated: use pod-level annotations.
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`

	// Labels that are added to Pilot pods.
	//
	// See https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/
	PodLabels map[string]string `json:"podLabels,omitempty"`

	// Specifies which installation configuration profile to apply.
	Profile *string `json:"profile,omitempty"`

	// Number of replicas in the Pilot Deployment.
	//
	// Deprecated: use autoscaling configuration.
	ReplicaCount *int `json:"replicaCount,omitempty"`

	// K8s resources settings.
	//
	// See https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container
	//
	// Deprecated: use pod-level resource configuration.
	Resources *DiscoveryValuesResources `json:"resources,omitempty"`

	// Identifies the revision this installation is associated with.
	Revision *string `json:"revision,omitempty"`

	// Specifies the aliases for the Istio control plane revision. A MutatingWebhookConfiguration
	// is created for each alias.
	RevisionTags []string `json:"revisionTags,omitempty"`

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

	// The seccompProfile for the Pilot container.
	//
	// See: https://kubernetes.io/docs/tutorials/security/seccomp/
	SeccompProfile *corev1.SeccompProfile `json:"seccompProfile,omitempty"`

	// K8s annotations for the service account.
	ServiceAccountAnnotations map[string]string `json:"serviceAccountAnnotations,omitempty"`

	// K8s annotations for the Service.
	//
	// See: https://kubernetes.io/docs/concepts/overview/working-with-objects/annotations/
	ServiceAnnotations map[string]string `json:"serviceAnnotations,omitempty"`

	// Configuration for the sidecar injector webhook.
	SidecarInjectorWebhook *DiscoveryValuesSidecarInjectorWebhook `json:"sidecarInjectorWebhook,omitempty"`

	// Annotations for the sidecar injector webhook.
	SidecarInjectorWebhookAnnotations map[string]any `json:"sidecarInjectorWebhookAnnotations,omitempty"`

	// The container image tag to pull. Image will be `Hub/Image:Tag-Variant`.
	Tag any `json:"tag,omitempty"`

	// Configures the taint controller for new nodes.
	Taint *DiscoveryValuesTaint `json:"taint,omitempty"`

	// Controls whether telemetry is exported for Pilot.
	Telemetry *ValuesTelemetry `json:"telemetry,omitempty"`

	// The node tolerations to be applied to the Pilot deployment so that it can be
	// scheduled to particular nodes with matching taints.
	//
	// More info: https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/pod-v1/#scheduling
	//
	// Deprecated: use pod-level toleration configuration.
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// The k8s topologySpreadConstraints for the Pilot pods.
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`

	// Trace sampling fraction.
	//
	// Used to set the fraction of time that traces are sampled. Higher values are more accurate but add CPU overhead.
	//
	// Allowed values: 0.0 to 1.0
	TraceSampling *float64 `json:"traceSampling,omitempty"`

	// The name of the trusted ztunnel instance.
	TrustedZtunnelName *string `json:"trustedZtunnelName,omitempty"`

	// If set, istiod will allow connections from trusted node proxy ztunnels
	// in the provided namespace.
	TrustedZtunnelNamespace *string `json:"trustedZtunnelNamespace,omitempty"`

	// The container image variant to pull. Options are "debug" or "distroless".
	// Unset will use the default for the given version.
	Variant *string `json:"variant,omitempty"`

	// Additional volumeMounts to add to the Pilot container.
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// Additional volumes to add to the Pilot Pod.
	Volumes []corev1.Volume `json:"volumes,omitempty"`
}

// DiscoveryValuesBase is the configuration for the base chart.
type DiscoveryValuesBase struct {
	// For istioctl usage to disable istio config crds in base.
	EnableIstioConfigCRDs *bool `json:"enableIstioConfigCRDs,omitempty"`
}

// DiscoveryValuesCni configures whether CNI should be used.
type DiscoveryValuesCni struct {
	// Controls whether CNI should be used.
	Enabled *bool `json:"enabled,omitempty"`

	// Specifies the CNI provider. Can be either "default" or "multus". When set to "multus", an annotation
	// `k8s.v1.cni.cncf.io/networks` is set on injected pods to point to a NetworkAttachmentDefinition.
	Provider *string `json:"provider,omitempty"`
}

// DiscoveryValuesCpu is the configuration for CPU target utilization for HorizontalPodAutoscaler target.
type DiscoveryValuesCpu struct {
	// K8s utilization setting for HorizontalPodAutoscaler target.
	//
	// See https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/
	TargetAverageUtilization *int `json:"targetAverageUtilization,omitempty"`
}

// DiscoveryValuesEnvVarFromElem represents an environment variable sourced from a ConfigMap or Secret.
type DiscoveryValuesEnvVarFromElem struct {
	// The name of the environment variable.
	Name *string `json:"name,omitempty"`

	// The source of the environment variable's value.
	ValueFrom map[string]any `json:"valueFrom,omitempty"`
}

// DiscoveryValuesGateways is the configuration for gateways.
type DiscoveryValuesGateways struct {
	// Configures the seccomp profile for gateway containers.
	SeccompProfile *corev1.SeccompProfile `json:"seccompProfile,omitempty"`

	// Security context for gateway containers.
	SecurityContext *corev1.PodSecurityContext `json:"securityContext,omitempty"`
}

// DiscoveryValuesIstiodRemote is the configuration for istiod-remote.
type DiscoveryValuesIstiodRemote struct {
	// Indicates if this cluster/install should consume a "remote" istiod instance.
	Enabled *bool `json:"enabled,omitempty"`

	// If true, indicates that this cluster/install should consume a "local istiod" installation,
	// local istiod inject sidecars.
	EnabledLocalInjectorIstiod *bool `json:"enabledLocalInjectorIstiod,omitempty"`

	// Injector CA bundle.
	InjectionCABundle *string `json:"injectionCABundle,omitempty"`

	// Path to use for the sidecar injector webhook service.
	InjectionPath *string `json:"injectionPath,omitempty"`

	// URL to use for sidecar injector webhook.
	InjectionURL *string `json:"injectionURL,omitempty"`
}

// DiscoveryValuesMemory is the configuration for memory target utilization for HorizontalPodAutoscaler target.
type DiscoveryValuesMemory struct {
	// K8s utilization setting for HorizontalPodAutoscaler target.
	//
	// See https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/
	TargetAverageUtilization *int `json:"targetAverageUtilization,omitempty"`
}

// DiscoveryValuesMeshConfig defines runtime configuration of components, including Istiod and istio-agent behavior.
type DiscoveryValuesMeshConfig struct {
	// Controls whether Prometheus metrics merging is enabled.
	EnablePrometheusMerge *bool `json:"enablePrometheusMerge,omitempty"`
}

// DiscoveryValuesPdb is the pod disruption budget configuration.
type DiscoveryValuesPdb struct {
	// The maximum number of pods that can be unavailable during disruption.
	MaxUnavailable *IntOrString `json:"maxUnavailable,omitempty"`

	// The minimum number of pods that must be available during disruption.
	MinAvailable *IntOrString `json:"minAvailable,omitempty"`

	// The policy for evicting unhealthy pods.
	UnhealthyPodEvictionPolicy *string `json:"unhealthyPodEvictionPolicy,omitempty"`
}

// DiscoveryValuesResources defines compute resources required by a container.
type DiscoveryValuesResources struct {
	// The maximum amount of compute resources allowed.
	//
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/
	Limits *DiscoveryValuesResourcesLimits `json:"limits,omitempty"`

	// The minimum amount of compute resources required.
	//
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/
	Requests *DiscoveryValuesResourcesRequests `json:"requests,omitempty"`
}

// DiscoveryValuesResourcesLimits specifies the maximum amount of compute resources allowed.
type DiscoveryValuesResourcesLimits struct {
	// CPU resource limit.
	Cpu *string `json:"cpu,omitempty"`

	// Memory resource limit.
	Memory *string `json:"memory,omitempty"`
}

// DiscoveryValuesResourcesRequests specifies the minimum amount of compute resources required.
type DiscoveryValuesResourcesRequests struct {
	// CPU resource request.
	Cpu *string `json:"cpu,omitempty"`

	// Memory resource request.
	Memory *string `json:"memory,omitempty"`
}

// DiscoveryValuesSidecarInjectorWebhook is the configuration for the sidecar injector webhook.
type DiscoveryValuesSidecarInjectorWebhook struct {
	// See NeverInjectSelector.
	AlwaysInjectSelector []any `json:"alwaysInjectSelector,omitempty"`

	// Default templates specifies a set of default templates that are used in sidecar injection.
	// By default, a template `sidecar` is always provided, which contains the template of default sidecar.
	// To inject other additional templates, define it using the `templates` option, and add it to
	// the default templates list.
	DefaultTemplates []any `json:"defaultTemplates,omitempty"`

	// Enables sidecar auto-injection in namespaces by default.
	EnableNamespacesByDefault *bool `json:"enableNamespacesByDefault,omitempty"`

	// injectedAnnotations are additional annotations that will be added to the pod spec after injection.
	// This is primarily to support PSP annotations.
	InjectedAnnotations map[string]any `json:"injectedAnnotations,omitempty"`

	// Instructs Istio to not inject the sidecar on those pods, based on labels that are present in those pods.
	//
	// Annotations in the pods have higher precedence than the label selectors.
	// Order of evaluation: Pod Annotations -> NeverInjectSelector -> AlwaysInjectSelector -> Default Policy.
	// See https://istio.io/docs/setup/kubernetes/additional-setup/sidecar-injection/#more-control-adding-exceptions
	NeverInjectSelector []any `json:"neverInjectSelector,omitempty"`

	// Setting this to `IfNeeded` will result in the sidecar injector being run again if additional
	// mutations occur. Default: Never
	ReinvocationPolicy *string `json:"reinvocationPolicy,omitempty"`

	// If true, webhook or istioctl injector will rewrite PodSpec for liveness health check to redirect
	// request to sidecar. This makes liveness check work even when mTLS is enabled.
	RewriteAppHTTPProbe *bool `json:"rewriteAppHTTPProbe,omitempty"`

	// Templates defines a set of custom injection templates that can be used. For example, defining:
	//
	//	templates:
	//	  hello: |
	//	    metadata:
	//	      labels:
	//	        hello: world
	//
	// Then starting a pod with the `inject.istio.io/templates: hello` annotation, will result in the pod
	// being injected with the hello=world labels.
	// This is intended for advanced configuration only; most users should use the built in template.
	Templates map[string]any `json:"templates,omitempty"`
}

// DiscoveryValuesTaint configures the taint controller.
type DiscoveryValuesTaint struct {
	// Enable the untaint controller for new nodes. This aims to solve a race for CNI installation on
	// new nodes. For this to work, the newly added nodes need to have the istio CNI taint as they are
	// added to the cluster. This is usually done by configuring the cluster infra provider.
	Enabled *bool `json:"enabled,omitempty"`

	// The namespace of the CNI daemonset, in case it's not the same as istiod.
	Namespace *string `json:"namespace,omitempty"`
}
