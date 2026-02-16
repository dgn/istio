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

type CniValues struct {
	// Internal defaults that should not be set by users.
	InternalDefaultsDoNotSet map[string]any `json:"_internal_defaults_do_not_set,omitempty"`

	// K8s affinity to set on the istio-cni Pods. Can be used to exclude istio-cni from being scheduled on specified nodes.
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// Configuration for Istio Ambient.
	Ambient *CniValuesAmbient `json:"ambient,omitempty"`

	// Configure the plugin as a chained CNI plugin. When true, the configuration is added to the CNI chain; when false,
	// the configuration is added as a standalone file in the CNI configuration directory.
	Chained *bool `json:"chained,omitempty"`

	// The directory path within the cluster node's filesystem where the CNI binaries are to be installed. Typically /var/lib/cni/bin.
	CniBinDir *string `json:"cniBinDir,omitempty"`

	// The directory path within the cluster node's filesystem where the CNI configuration files are to be installed. Typically /etc/cni/net.d.
	CniConfDir *string `json:"cniConfDir,omitempty"`

	// The name of the CNI plugin configuration file. Defaults to istio-cni.conf.
	CniConfFileName *string `json:"cniConfFileName,omitempty"`

	// The directory path within the cluster node's filesystem where network namespaces are located.
	// Defaults to '/var/run/netns', in minikube/docker/others can be '/var/run/docker/netns'.
	CniNetnsDir *string `json:"cniNetnsDir,omitempty"`

	// Specifies the compatibility version to use. When this is set, the control plane will
	// be configured with the same defaults as the specified version.
	CompatibilityVersion *string `json:"compatibilityVersion,omitempty"`

	// Additional labels to apply to the istio-cni DaemonSet.
	DaemonSetLabels map[string]string `json:"daemonSetLabels,omitempty"`

	// The name of the default revision in the cluster.
	DefaultRevision *string `json:"defaultRevision,omitempty"`

	// Controls whether CNI is installed.
	Enabled *bool `json:"enabled,omitempty"`

	// Environment variables passed to the CNI container.
	//
	// Examples:
	// env:
	//   ENV_VAR_1: value1
	//   ENV_VAR_2: value2
	Env map[string]string `json:"env,omitempty"`

	// List of namespaces that should be ignored by the CNI plugin.
	ExcludeNamespaces []string `json:"excludeNamespaces,omitempty"`

	// Global configuration for Istio components.
	Global *GlobalValues `json:"global,omitempty"`

	// Hub to pull the container image from. Image will be `Hub/Image:Tag-Variant`.
	Hub *string `json:"hub,omitempty"`

	// Image name to pull from. Image will be `Hub/Image:Tag-Variant`.
	// If Image contains a "/", it will replace the entire `image` in the pod.
	Image *string `json:"image,omitempty"`

	// Specifies if an Istio owned CNI config should be created.
	IstioOwnedCNIConfig *bool `json:"istioOwnedCNIConfig,omitempty"`

	// The file name for the Istio owned CNI configuration.
	IstioOwnedCNIConfigFileName *string `json:"istioOwnedCNIConfigFileName,omitempty"`

	// DEPRECATED. Configuration log level of istio-cni binary. By default, istio-cni sends all logs to the UDS server.
	// To see the logs, change global.logging.level to cni:debug.
	LogLevel *string `json:"logLevel,omitempty"`

	// Same as `global.logging.level`, but will override it if set.
	Logging *CniValuesLogging `json:"logging,omitempty"`

	// Used internally to identify the owner of each resource.
	OwnerName *string `json:"ownerName,omitempty"`

	// Platform in which Istio is deployed. Possible values are: "openshift" and "gcp".
	// An empty value means it is a vanilla Kubernetes distribution, therefore no special
	// treatment will be considered.
	Platform *string `json:"platform,omitempty"`

	// No longer used for CNI. See: https://github.com/istio/istio/issues/49004
	//
	// Deprecated: No longer used for CNI.
	Privileged *bool `json:"privileged,omitempty"`

	// Additional annotations to apply to the istio-cni Pods.
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`

	// Additional labels to apply to the istio-cni Pods.
	PodLabels map[string]string `json:"podLabels,omitempty"`

	// Specifies which installation configuration profile to apply.
	Profile *string `json:"profile,omitempty"`

	// Specifies the CNI provider. Can be either "default" or "multus". When set to "multus", an additional
	// NetworkAttachmentDefinition resource is deployed to the cluster to allow the istio-cni plugin to be
	// invoked in a cluster using the Multus CNI plugin.
	Provider *CniValuesProvider `json:"provider,omitempty"`

	// PodSecurityPolicy cluster role. No longer used anywhere.
	// Deprecated: PSP is removed in Kubernetes 1.25+.
	PspClusterRole *string `json:"psp_cluster_role,omitempty"`

	// Specifies the image pull policy. One of Always, Never, IfNotPresent.
	// Defaults to Always if :latest tag is specified, or IfNotPresent otherwise. Cannot be updated.
	//
	// More info: https://kubernetes.io/docs/concepts/containers/images#updating-images
	PullPolicy *ImagePullPolicy `json:"pullPolicy,omitempty"`

	// Configuration for the CNI Repair controller.
	Repair *CniValuesRepair `json:"repair,omitempty"`

	// The resource quotas configuration for the CNI DaemonSet.
	ResourceQuotas *CniValuesResourceQuotas `json:"resourceQuotas,omitempty"`

	// The k8s resource requests and limits for the istio-cni Pods.
	Resources *CniValuesResources `json:"resources,omitempty"`

	// The number of pods that can be unavailable during a rolling update of the CNI DaemonSet (see
	// `updateStrategy.rollingUpdate.maxUnavailable` here:
	// https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/daemon-set-v1/#DaemonSetSpec).
	// May be specified as a number of pods or as a percent of the total number
	// of pods at the start of the update.
	RollingMaxUnavailable *IntOrString `json:"rollingMaxUnavailable,omitempty"`

	// Configures the revision this control plane is a part of.
	Revision *string `json:"revision,omitempty"`

	// The SELinux options for the CNI container.
	SeLinuxOptions *corev1.SELinuxOptions `json:"seLinuxOptions,omitempty"`

	// The Container seccompProfile.
	//
	// See: https://kubernetes.io/docs/tutorials/security/seccomp/
	SeccompProfile *corev1.SeccompProfile `json:"seccompProfile,omitempty"`

	// The container image tag to pull. Image will be `Hub/Image:Tag-Variant`.
	Tag any `json:"tag,omitempty"`

	// The termination grace period for the CNI pods, in seconds.
	TerminationGracePeriodSeconds *float64 `json:"terminationGracePeriodSeconds,omitempty"`

	// K8s node tolerations to be applied to the istio-cni Pods.
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// The update strategy for the CNI DaemonSet.
	UpdateStrategy *CniValuesUpdateStrategy `json:"updateStrategy,omitempty"`

	// The container image variant to pull. Options are "debug" or "distroless". Unset will use the default for the given version.
	Variant *string `json:"variant,omitempty"`
}

type CniValuesAmbient struct {
	// The directory path containing the configuration files for Ambient. Defaults to /etc/ambient-config.
	ConfigDir *string `json:"configDir,omitempty"`

	// If enabled, and ambient is enabled, DNS redirection will be enabled.
	DnsCapture *bool `json:"dnsCapture,omitempty"`

	// If enabled, enables ambient detection retry logic.
	EnableAmbientDetectionRetry *bool `json:"enableAmbientDetectionRetry,omitempty"`

	// Controls whether ambient redirection is enabled.
	Enabled *bool `json:"enabled,omitempty"`

	// Selectors used to determine which workloads are enrolled in ambient mode.
	EnablementSelectors []map[string]any `json:"enablementSelectors,omitempty"`

	// UNSTABLE: If enabled, and ambient is enabled, enables ipv6 support.
	Ipv6 *bool `json:"ipv6,omitempty"`

	// If enabled, and ambient is enabled, iptables reconciliation will be enabled.
	ReconcileIptablesOnStartup *bool `json:"reconcileIptablesOnStartup,omitempty"`

	// Controls whether the CNI agent shares the host network namespace.
	ShareHostNetworkNamespace *bool `json:"shareHostNetworkNamespace,omitempty"`
}

type CniValuesLogging struct {
	// Comma-separated minimum per-scope logging level of messages to output, in the form of <scope>:<level>,<scope>:<level>.
	// The control plane has different scopes depending on component, but can configure default log level across all components.
	// If empty, default scope and level will be used as configured in code.
	Level *string `json:"level,omitempty"`
}

type CniValuesProvider string

const CniValuesProviderDefault CniValuesProvider = "default"
const CniValuesProviderMultus CniValuesProvider = "multus"

type CniValuesRepair struct {
	// The label key to apply to a broken pod when the controller is in labelPods mode.
	BrokenPodLabelKey *string `json:"brokenPodLabelKey,omitempty"`

	// The label value to apply to a broken pod when the controller is in labelPods mode.
	BrokenPodLabelValue *string `json:"brokenPodLabelValue,omitempty"`

	// The Repair controller has 3 modes (labelPods, deletePods, and repairPods). Pick which one meets your use cases. Note only one may be used.
	// The mode defines the action the controller will take when a pod is detected as broken.
	// If deletePods is true, the controller will delete the broken pod. The pod will then be rescheduled, hopefully onto a node that is fully ready.
	// Note this gives the DaemonSet a relatively high privilege, as it can delete any Pod.
	DeletePods *bool `json:"deletePods,omitempty"`

	// Controls whether repair behavior is enabled.
	Enabled *bool `json:"enabled,omitempty"`

	// Hub to pull the container image from. Image will be `Hub/Image:Tag-Variant`.
	Hub *string `json:"hub,omitempty"`

	// The name of the init container to use for the repairPods mode.
	InitContainerName *string `json:"initContainerName,omitempty"`

	// The Repair controller has 3 modes (labelPods, deletePods, and repairPods). Pick which one meets your use cases. Note only one may be used.
	// The mode defines the action the controller will take when a pod is detected as broken.
	// If labelPods is true, the controller will label all broken pods with <brokenPodLabelKey>=<brokenPodLabelValue>.
	// This is only capable of identifying broken pods; the user is responsible for fixing them (generally, by deleting them).
	// Note this gives the DaemonSet a relatively high privilege, as modifying pod metadata/status can have wider impacts.
	LabelPods *bool `json:"labelPods,omitempty"`

	// The Repair controller has 3 modes (labelPods, deletePods, and repairPods). Pick which one meets your use cases. Note only one may be used.
	// The mode defines the action the controller will take when a pod is detected as broken.
	// If repairPods is true, the controller will dynamically repair any broken pod by setting up the pod networking configuration even after it has started.
	// Note the pod will be crashlooping, so this may take a few minutes to become fully functional based on when the retry occurs.
	// This requires no RBAC privilege, but will require the CNI agent to run as a privileged pod.
	RepairPods *bool `json:"repairPods,omitempty"`

	// The container image tag to pull. Image will be `Hub/Image:Tag-Variant`.
	Tag any `json:"tag,omitempty"`
}

type CniValuesResourceQuotas struct {
	// Controls whether to create resource quotas or not for the CNI DaemonSet.
	Enabled *bool `json:"enabled,omitempty"`

	// The hard limit on the number of pods in the namespace where the CNI DaemonSet is deployed.
	Pods *int `json:"pods,omitempty"`
}

type CniValuesResources struct {
	// The maximum amount of compute resources allowed.
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/
	Limits *CniValuesResourcesLimits `json:"limits,omitempty"`

	// The minimum amount of compute resources required. If Requests is omitted for a container,
	// it defaults to Limits if that is explicitly specified, otherwise to an implementation-defined value.
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/
	Requests *CniValuesResourcesRequests `json:"requests,omitempty"`
}

type CniValuesResourcesLimits struct {
	// CPU resource limit.
	Cpu *string `json:"cpu,omitempty"`

	// Memory resource limit.
	Memory *string `json:"memory,omitempty"`
}

type CniValuesResourcesRequests struct {
	// CPU resource request.
	Cpu *string `json:"cpu,omitempty"`

	// Memory resource request.
	Memory *string `json:"memory,omitempty"`
}

type CniValuesUpdateStrategy struct {
	// Rolling update configuration for the DaemonSet.
	RollingUpdate *CniValuesUpdateStrategyRollingUpdate `json:"rollingUpdate,omitempty"`

	// The update strategy type. Can be "RollingUpdate" or "OnDelete".
	Type *string `json:"type,omitempty"`
}

type CniValuesUpdateStrategyRollingUpdate struct {
	// The maximum number of pods that can be unavailable during the rolling update.
	MaxUnavailable *IntOrString `json:"maxUnavailable,omitempty"`
}

// Nil-safe accessor methods for CniValues.

func (v *CniValues) GetAffinity() *corev1.Affinity {
	if v == nil {
		return nil
	}
	return v.Affinity
}

func (v *CniValues) GetSeccompProfile() *corev1.SeccompProfile {
	if v == nil {
		return nil
	}
	return v.SeccompProfile
}
