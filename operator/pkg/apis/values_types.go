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

// Configuration affecting Istio control plane installation version and shape.

package apis

import (
	corev1 "k8s.io/api/core/v1"
)

// Values defines the configuration values for all Istio components.
// This is the top-level type that maps to the IstioOperator spec.values field.
type Values struct {
	// Configuration for the Istio CNI plugin.
	Cni *CniValues `json:"cni,omitempty"`

	// Configuration for ingress and egress gateways.
	Gateways *GatewaysConfig `json:"gateways,omitempty"`

	// Global configuration for Istio components.
	Global *GlobalValues `json:"global,omitempty"`

	// Configuration for the Pilot component.
	Pilot *DiscoveryValues `json:"pilot,omitempty"`

	// Configuration for the ZTunnel component.
	Ztunnel *ZtunnelValues `json:"ztunnel,omitempty"`

	// Controls whether telemetry is exported for Pilot.
	Telemetry *ValuesTelemetry `json:"telemetry,omitempty"`

	// Configuration for the sidecar injector webhook.
	SidecarInjectorWebhook *SidecarInjectorConfig `json:"sidecarInjectorWebhook,omitempty"`

	// Configuration for the Istio CNI plugin.
	//
	// Deprecated: use Cni instead.
	IstioCni *CNIUsageConfig `json:"istio_cni,omitempty"`

	// Identifies the revision this installation is associated with.
	Revision string `json:"revision,omitempty"`

	// Used internally to identify the owner of each resource.
	OwnerName string `json:"ownerName,omitempty"`

	// Defines runtime configuration of components, including Istiod and istio-agent behavior.
	// See https://istio.io/docs/reference/config/istio.mesh.v1alpha1/ for all available options.
	MeshConfig any `json:"meshConfig,omitempty"`

	// Configuration for the base component.
	Base *BaseConfig `json:"base,omitempty"`

	// Configuration for istiod-remote.
	// DEPRECATED - istiod-remote chart is removed and replaced with
	// `istio-discovery --set values.istiodRemote.enabled=true`
	IstiodRemote *IstiodRemoteConfig `json:"istiodRemote,omitempty"`

	// Specifies the aliases for the Istio control plane revision. A MutatingWebhookConfiguration
	// is created for each alias.
	RevisionTags []string `json:"revisionTags,omitempty"`

	// The name of the default revision in the cluster.
	DefaultRevision string `json:"defaultRevision,omitempty"`

	// Specifies which installation configuration profile to apply.
	Profile string `json:"profile,omitempty"`

	// Specifies the compatibility version to use. When this is set, the control plane will
	// be configured with the same defaults as the specified version.
	CompatibilityVersion string `json:"compatibilityVersion,omitempty"`

	// Specifies experimental helm fields that could be removed or changed in the future
	Experimental *ValuesExperimental `json:"experimental,omitempty"`

	// Configuration for Gateway Classes.
	GatewayClasses any `json:"gatewayClasses,omitempty"`
}

// CNIUsageConfig configures whether CNI should be used.
type CNIUsageConfig struct {
	// Controls whether CNI should be used.
	Enabled *bool `json:"enabled,omitempty"`
	// Deprecated: no longer used.
	Chained *bool `json:"chained,omitempty"`
	// Specifies the CNI provider. Can be either "default" or "multus". When set to "multus", an annotation
	// `k8s.v1.cni.cncf.io/networks` is set on injected pods to point to a NetworkAttachmentDefinition.
	Provider string `json:"provider,omitempty"`
}

// TargetUtilizationConfig is the configuration for CPU or memory target utilization
// for HorizontalPodAutoscaler target.
type TargetUtilizationConfig struct {
	// K8s utilization setting for HorizontalPodAutoscaler target.
	//
	// See https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/
	TargetAverageUtilization int `json:"targetAverageUtilization,omitempty"`
}

// ServiceAccount mirrors the Kubernetes ServiceAccount for unmarshaling.
type ServiceAccount struct {
	Annotations map[string]any `json:"annotations,omitempty"`
}

// TracerConfig specifies the configuration for each of the supported tracers.
type TracerConfig struct {
	// Configuration for the datadog tracing service.
	Datadog *TracerDatadogConfig `json:"datadog,omitempty"`
	// Configuration for the lightstep tracing service.
	Lightstep *TracerLightStepConfig `json:"lightstep,omitempty"`
	// Configuration for the zipkin tracing service.
	Zipkin *TracerZipkinConfig `json:"zipkin,omitempty"`
	// Configuration for the stackdriver tracing service.
	Stackdriver *TracerStackdriverConfig `json:"stackdriver,omitempty"`
}

// TracerDatadogConfig is the configuration for the datadog tracing service.
type TracerDatadogConfig struct {
	// Address in host:port format for reporting trace data to the Datadog agent.
	Address string `json:"address,omitempty"`
}

// TracerLightStepConfig is the configuration for the lightstep tracing service.
type TracerLightStepConfig struct {
	// Sets the lightstep satellite pool address in host:port format for reporting trace data.
	Address string `json:"address,omitempty"`
	// Sets the lightstep access token.
	AccessToken string `json:"accessToken,omitempty"`
}

// TracerZipkinConfig is the configuration for the zipkin tracing service.
type TracerZipkinConfig struct {
	// Address of zipkin instance in host:port format for reporting trace data.
	//
	// Example: <zipkin-collector-service>.<zipkin-collector-namespace>:941
	Address string `json:"address,omitempty"`
}

// TracerStackdriverConfig is the configuration for the stackdriver tracing service.
type TracerStackdriverConfig struct {
	// Enables trace output to stdout.
	Debug *bool `json:"debug,omitempty"`
	// The global default max number of attributes per span.
	MaxNumberOfAttributes int `json:"maxNumberOfAttributes,omitempty"`
	// The global default max number of annotation events per span.
	MaxNumberOfAnnotations int `json:"maxNumberOfAnnotations,omitempty"`
	// The global default max number of message events per span.
	MaxNumberOfMessageEvents int `json:"maxNumberOfMessageEvents,omitempty"`
}

// GatewaysConfig is the configuration for gateways.
type GatewaysConfig struct {
	// Configuration for an egress gateway.
	IstioEgressgateway *EgressGatewayConfig `json:"istio-egressgateway,omitempty"`
	// Controls whether any gateways are enabled.
	Enabled *bool `json:"enabled,omitempty"`
	// Configuration for an ingress gateway.
	IstioIngressgateway *IngressGatewayConfig `json:"istio-ingressgateway,omitempty"`
	SecurityContext     *corev1.PodSecurityContext `json:"securityContext,omitempty"`
	SeccompProfile      *corev1.SeccompProfile     `json:"seccompProfile,omitempty"`
}

// IngressGatewayConfig is the configuration for an ingress gateway.
type IngressGatewayConfig struct {
	// Controls whether auto scaling with a HorizontalPodAutoscaler is enabled.
	AutoscaleEnabled *bool `json:"autoscaleEnabled,omitempty"`
	// maxReplicas setting for HorizontalPodAutoscaler.
	AutoscaleMax int `json:"autoscaleMax,omitempty"`
	// minReplicas setting for HorizontalPodAutoscaler.
	AutoscaleMin int `json:"autoscaleMin,omitempty"`
	// K8s memory utilization setting for HorizontalPodAutoscaler target.
	//
	// Deprecated: See https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/
	Memory *TargetUtilizationConfig `json:"memory,omitempty"`
	// K8s cpu utilization setting for HorizontalPodAutoscaler target.
	//
	// Deprecated: See https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/
	Cpu           *TargetUtilizationConfig `json:"cpu,omitempty"`
	CustomService *bool                    `json:"customService,omitempty"`
	// Controls whether an ingress gateway is enabled.
	Enabled *bool `json:"enabled,omitempty"`
	// Environment variables passed to the proxy container.
	Env    map[string]any    `json:"env,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
	LoadBalancerIP          string   `json:"loadBalancerIP,omitempty"`
	LoadBalancerSourceRanges []string `json:"loadBalancerSourceRanges,omitempty"`
	Name string `json:"name,omitempty"`
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
	// See EgressGatewayConfig.
	//
	// Deprecated: use pod-level affinity configuration.
	PodAntiAffinityLabelSelector []map[string]any `json:"podAntiAffinityLabelSelector,omitempty"`
	// See EgressGatewayConfig.
	//
	// Deprecated: use pod-level affinity configuration.
	PodAntiAffinityTermLabelSelector []map[string]any `json:"podAntiAffinityTermLabelSelector,omitempty"`
	// Port Configuration for the ingress gateway.
	Ports []PortsConfig `json:"ports,omitempty"`
	// Number of replicas for the ingress gateway Deployment.
	//
	// Deprecated: use autoscaling configuration.
	ReplicaCount int `json:"replicaCount,omitempty"`
	// K8s resources settings.
	//
	// See https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container
	//
	// Deprecated: use pod-level resource configuration.
	Resources *Resources `json:"resources,omitempty"`
	// Config for secret volume mounts.
	SecretVolumes []SecretVolume `json:"secretVolumes,omitempty"`
	// Annotations to add to the ingress gateway service.
	ServiceAnnotations map[string]string `json:"serviceAnnotations,omitempty"`
	// Service type.
	//
	// See https://kubernetes.io/docs/concepts/services-networking/service/#publishing-services-service-types
	Type string `json:"type,omitempty"`
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
	ExternalTrafficPolicy string              `json:"externalTrafficPolicy,omitempty"`
	// Deprecated: use pod-level toleration configuration.
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
	IngressPorts         []map[string]any `json:"ingressPorts,omitempty"`
	AdditionalContainers []corev1.Container `json:"additionalContainers,omitempty"`
	ConfigVolumes        []corev1.Volume `json:"configVolumes,omitempty"`
	RunAsRoot            *bool            `json:"runAsRoot,omitempty"`
	// The injection template to use for the gateway. If not set, no injection will be performed.
	InjectionTemplate string          `json:"injectionTemplate,omitempty"`
	ServiceAccount    *ServiceAccount `json:"serviceAccount,omitempty"`
	// Defines which IP family to use for single stack or the order of IP families for dual-stack.
	// Valid list items are "IPv4", "IPv6".
	//
	// More info: https://kubernetes.io/docs/concepts/services-networking/dual-stack/#services
	IpFamilies []string `json:"ipFamilies,omitempty"`
	// Controls whether Services are configured to use IPv4, IPv6, or both. Valid options
	// are PreferDualStack, RequireDualStack, and SingleStack.
	//
	// More info: https://kubernetes.io/docs/concepts/services-networking/dual-stack/#services
	IpFamilyPolicy string `json:"ipFamilyPolicy,omitempty"`
}

// EgressGatewayConfig is the configuration for an egress gateway.
type EgressGatewayConfig struct {
	// Controls whether auto scaling with a HorizontalPodAutoscaler is enabled.
	AutoscaleEnabled *bool `json:"autoscaleEnabled,omitempty"`
	// maxReplicas setting for HorizontalPodAutoscaler.
	AutoscaleMax int `json:"autoscaleMax,omitempty"`
	// minReplicas setting for HorizontalPodAutoscaler.
	AutoscaleMin int `json:"autoscaleMin,omitempty"`
	// K8s memory utilization setting for HorizontalPodAutoscaler target.
	//
	// Deprecated: See https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/
	Memory *TargetUtilizationConfig `json:"memory,omitempty"`
	// K8s cpu utilization setting for HorizontalPodAutoscaler target.
	//
	// Deprecated: See https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/
	Cpu           *TargetUtilizationConfig `json:"cpu,omitempty"`
	CustomService *bool                    `json:"customService,omitempty"`
	// Controls whether an egress gateway is enabled.
	Enabled *bool `json:"enabled,omitempty"`
	// Environment variables passed to the proxy container.
	Env    map[string]any    `json:"env,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
	Name   string            `json:"name,omitempty"`
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
	// Ports Configuration for the egress gateway service.
	Ports []PortsConfig `json:"ports,omitempty"`
	// K8s resources settings.
	//
	// See https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container
	//
	// Deprecated: use pod-level resource configuration.
	Resources *Resources `json:"resources,omitempty"`
	// Config for secret volume mounts.
	SecretVolumes []SecretVolume `json:"secretVolumes,omitempty"`
	// Annotations to add to the egress gateway service.
	ServiceAnnotations map[string]string `json:"serviceAnnotations,omitempty"`
	// Service type.
	//
	// See https://kubernetes.io/docs/concepts/services-networking/service/#publishing-services-service-types
	Type string `json:"type,omitempty"`
	// Deprecated: use pod-level toleration configuration.
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
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
	ConfigVolumes         []corev1.Volume     `json:"configVolumes,omitempty"`
	AdditionalContainers  []corev1.Container  `json:"additionalContainers,omitempty"`
	RunAsRoot             *bool               `json:"runAsRoot,omitempty"`
	// The injection template to use for the gateway. If not set, no injection will be performed.
	InjectionTemplate string          `json:"injectionTemplate,omitempty"`
	ServiceAccount    *ServiceAccount `json:"serviceAccount,omitempty"`
	// Defines which IP family to use for single stack or the order of IP families for dual-stack.
	// Valid list items are "IPv4", "IPv6".
	//
	// More info: https://kubernetes.io/docs/concepts/services-networking/dual-stack/#services
	IpFamilies []string `json:"ipFamilies,omitempty"`
	// Controls whether Services are configured to use IPv4, IPv6, or both. Valid options
	// are PreferDualStack, RequireDualStack, and SingleStack.
	//
	// More info: https://kubernetes.io/docs/concepts/services-networking/dual-stack/#services
	IpFamilyPolicy string `json:"ipFamilyPolicy,omitempty"`
}

// PortsConfig is the configuration for a port.
type PortsConfig struct {
	// Port name.
	Name string `json:"name,omitempty"`
	// Port number.
	Port int `json:"port,omitempty"`
	// NodePort number.
	NodePort int `json:"nodePort,omitempty"`
	// Target port number.
	TargetPort int `json:"targetPort,omitempty"`
	// Protocol name.
	Protocol string `json:"protocol,omitempty"`
}

// SecretVolume is the configuration for secret volume mounts.
//
// See https://kubernetes.io/docs/concepts/configuration/secret/#using-secrets.
type SecretVolume struct {
	MountPath  string `json:"mountPath,omitempty"`
	Name       string `json:"name,omitempty"`
	SecretName string `json:"secretName,omitempty"`
}

// SidecarInjectorConfig is the configuration for the sidecar injector webhook.
type SidecarInjectorConfig struct {
	// Enables sidecar auto-injection in namespaces by default.
	EnableNamespacesByDefault *bool `json:"enableNamespacesByDefault,omitempty"`
	// Setting this to `IfNeeded` will result in the sidecar injector being run again if additional
	// mutations occur. Default: Never
	ReinvocationPolicy string `json:"reinvocationPolicy,omitempty"`
	// Instructs Istio to not inject the sidecar on those pods, based on labels that are present in those pods.
	//
	// Annotations in the pods have higher precedence than the label selectors.
	// Order of evaluation: Pod Annotations -> NeverInjectSelector -> AlwaysInjectSelector -> Default Policy.
	// See https://istio.io/docs/setup/kubernetes/additional-setup/sidecar-injection/#more-control-adding-exceptions
	NeverInjectSelector []map[string]any `json:"neverInjectSelector,omitempty"`
	// See NeverInjectSelector.
	AlwaysInjectSelector []map[string]any `json:"alwaysInjectSelector,omitempty"`
	// If true, webhook or istioctl injector will rewrite PodSpec for liveness health check to redirect
	// request to sidecar. This makes liveness check work even when mTLS is enabled.
	RewriteAppHTTPProbe *bool `json:"rewriteAppHTTPProbe,omitempty"`
	// injectedAnnotations are additional annotations that will be added to the pod spec after injection.
	// This is primarily to support PSP annotations.
	InjectedAnnotations map[string]any `json:"injectedAnnotations,omitempty"`
	// Configure the injection url for sidecar injector webhook.
	InjectionURL string `json:"injectionURL,omitempty"`
	// Templates defines a set of custom injection templates that can be used.
	Templates map[string]any `json:"templates,omitempty"`
	// Default templates specifies a set of default templates that are used in sidecar injection.
	DefaultTemplates []string `json:"defaultTemplates,omitempty"`
}

// IstiodRemoteConfig is the configuration for istiod-remote.
type IstiodRemoteConfig struct {
	// URL to use for sidecar injector webhook.
	InjectionURL string `json:"injectionURL,omitempty"`
	// Path to use for the sidecar injector webhook service.
	InjectionPath string `json:"injectionPath,omitempty"`
	// Injector CA bundle.
	InjectionCABundle string `json:"injectionCABundle,omitempty"`
	// Indicates if this cluster/install should consume a "remote" istiod instance.
	Enabled *bool `json:"enabled,omitempty"`
	// If true, indicates that this cluster/install should consume a "local istiod" installation,
	// local istiod inject sidecars.
	EnabledLocalInjectorIstiod *bool `json:"enabledLocalInjectorIstiod,omitempty"`
}

// BaseConfig is the configuration for the base chart.
type BaseConfig struct {
	// For Helm2 use, adds the CRDs to templates.
	EnableCRDTemplates *bool `json:"enableCRDTemplates,omitempty"`
	// CRDs to exclude. Requires `enableCRDTemplates`.
	ExcludedCRDs []string `json:"excludedCRDs,omitempty"`
	// URL to use for validating webhook.
	ValidationURL string `json:"validationURL,omitempty"`
	// For istioctl usage to disable istio config crds in base.
	EnableIstioConfigCRDs *bool `json:"enableIstioConfigCRDs,omitempty"`
	ValidateGateway       *bool `json:"validateGateway,omitempty"`
	// Validation webhook CA bundle.
	ValidationCABundle string `json:"validationCABundle,omitempty"`
}

// PilotIngressConfig controls legacy k8s ingress.
type PilotIngressConfig struct {
	// Sets the type ingress service for Pilot.
	//
	// If empty, node-port is assumed.
	//
	// Allowed values: node-port, istio-ingressgateway, ingress
	IngressService        string `json:"ingressService,omitempty"`
	IngressControllerMode string `json:"ingressControllerMode,omitempty"`
	// If mode is STRICT, this value must be set on "kubernetes.io/ingress.class" annotation to activate.
	IngressClass string `json:"ingressClass,omitempty"`
}

// PilotPolicyConfig controls whether Istio policy is applied to Pilot.
type PilotPolicyConfig struct {
	// Controls whether Istio policy is applied to Pilot.
	Enabled *bool `json:"enabled,omitempty"`
}

// OutboundTrafficPolicyConfig controls the default behavior of the sidecar for
// handling outbound traffic from the application.
type OutboundTrafficPolicyConfig struct {
	// Specifies the sidecar's default behavior when handling outbound traffic from the application.
	Mode string `json:"mode,omitempty"`
}
