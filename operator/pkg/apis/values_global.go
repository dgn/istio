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

// GlobalValues is the global configuration for Istio components.
type GlobalValues struct {
	// Specifies pod scheduling arch(amd64, ppc64le, s390x, arm64) and weight as follows:
	//   0 - Never scheduled
	//   1 - Least preferred
	//   2 - No preference
	//   3 - Most preferred
	//
	// Deprecated: replaced by the affinity k8s settings which allows architecture nodeAffinity
	// configuration of this behavior.
	Arch *ArchConfig `json:"arch,omitempty"`
	// The address of the CA for CSR.
	CaAddress string `json:"caAddress,omitempty"`
	// The name of the CA for workloads.
	// For example, when caName=GkeWorkloadCertificate, GKE workload certificates
	// will be used as the certificates for workloads.
	// The default value is "" and when caName="", the CA will be configured by other
	// mechanisms (e.g., environmental variable CA_PROVIDER).
	CaName string `json:"caName,omitempty"`
	// List of certSigners to allow "approve" action in the ClusterRole.
	CertSigners []string `json:"certSigners,omitempty"`
	// Controls whether a remote cluster is the config cluster for an external istiod.
	ConfigCluster *bool `json:"configCluster,omitempty"`
	// Controls whether the server-side validation is enabled.
	ConfigValidation *bool `json:"configValidation,omitempty"`
	// Default k8s node selector for all the Istio control plane components.
	//
	// See https://kubernetes.io/docs/concepts/configuration/assign-pod-node/#nodeselector
	//
	// Deprecated: use pod-level node selection.
	DefaultNodeSelector map[string]string `json:"defaultNodeSelector,omitempty"`
	// Specifies the default pod disruption budget configuration.
	//
	// See https://kubernetes.io/docs/concepts/workloads/pods/disruptions/
	DefaultPodDisruptionBudget *DefaultPodDisruptionBudgetConfig `json:"defaultPodDisruptionBudget,omitempty"`
	// Default k8s resources settings for all Istio control plane components.
	//
	// See https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container
	//
	// Deprecated: use pod-level resource configuration.
	DefaultResources *DefaultResourcesConfig `json:"defaultResources,omitempty"`
	// Default node tolerations to be applied to all deployments so that all pods can be
	// scheduled to nodes with matching taints. Each component can overwrite
	// these default values by adding its tolerations block in the relevant section below
	// and setting the desired values.
	// Configure this field in case that all pods of Istio control plane are expected to
	// be scheduled to particular nodes with specified taints.
	//
	// Deprecated: use pod-level toleration configuration.
	DefaultTolerations []corev1.Toleration `json:"defaultTolerations,omitempty"`
	// Controls whether one external istiod is enabled.
	ExternalIstiod *bool `json:"externalIstiod,omitempty"`
	// Specifies the docker hub for Istio images.
	Hub string `json:"hub,omitempty"`
	// Specifies the image pull policy for the Istio images. One of Always, Never, IfNotPresent.
	// Defaults to Always if :latest tag is specified, or IfNotPresent otherwise. Cannot be updated.
	//
	// More info: https://kubernetes.io/docs/concepts/containers/images#updating-images
	ImagePullPolicy ImagePullPolicy `json:"imagePullPolicy,omitempty"`
	// ImagePullSecrets for the control plane ServiceAccount, list of secrets in the same namespace
	// to use for pulling any images in pods that reference this ServiceAccount.
	// Must be set for any cluster configured with private docker registry.
	ImagePullSecrets []string `json:"imagePullSecrets,omitempty"`
	// Specifies the default namespace for the Istio control plane components.
	IstioNamespace string `json:"istioNamespace,omitempty"`
	// Specifies the configuration of istiod.
	Istiod *IstiodConfig `json:"istiod,omitempty"`
	// Specifies whether istio components should output logs in json format by adding
	// --log_as_json argument to each container.
	LogAsJson *bool `json:"logAsJson,omitempty"`
	// Specifies the global logging level settings for the Istio control plane components.
	Logging *GlobalLoggingConfig `json:"logging,omitempty"`
	// The Mesh Identifier. It should be unique within the scope where
	// meshes will interact with each other, but it is not required to be
	// globally/universally unique. For example, if any of the following are true,
	// then two meshes must have different Mesh IDs:
	// - Meshes will have their telemetry aggregated in one place
	// - Meshes will be federated together
	// - Policy will be written referencing one mesh from the other
	//
	// If an administrator expects that any of these conditions may become true in
	// the future, they should ensure their meshes have different Mesh IDs
	// assigned.
	//
	// Within a multicluster mesh, each cluster must be (manually or auto)
	// configured to have the same Mesh ID value. If an existing cluster 'joins' a
	// multicluster mesh, it will need to be migrated to the new mesh ID. Details
	// of migration TBD, and it may be a disruptive operation to change the Mesh
	// ID post-install.
	//
	// If the mesh admin does not specify a value, Istio will use the value of the
	// mesh's Trust Domain. The best practice is to select a proper Trust Domain
	// value.
	MeshID string `json:"meshID,omitempty"`
	// Configure the mesh networks to be used by the Split Horizon EDS.
	//
	// The following example defines two networks with different endpoints association methods.
	// For `network1` all endpoints that their IP belongs to the provided CIDR range will be
	// mapped to network1. The gateway for this network example is specified by its public IP
	// address and port.
	// The second network, `network2`, in this example is defined differently with all endpoints
	// retrieved through the specified Multi-Cluster registry being mapped to network2. The
	// gateway is also defined differently with the name of the gateway service on the remote
	// cluster. The public IP for the gateway will be determined from that remote service (only
	// LoadBalancer gateway service type is currently supported, for a NodePort type gateway service,
	// it still need to be configured manually).
	//
	// meshNetworks:
	//
	// 	network1:
	// 	  endpoints:
	// 	  - fromCidr: "192.168.0.1/24"
	// 	  gateways:
	// 	  - address: 1.1.1.1
	// 	    port: 80
	// 	network2:
	// 	  endpoints:
	// 	  - fromRegistry: reg1
	// 	  gateways:
	// 	  - registryServiceName: istio-ingressgateway.istio-system.svc.cluster.local
	// 	    port: 443
	MeshNetworks map[string]any `json:"meshNetworks,omitempty"`
	// Controls whether the in-cluster MTLS key and certs are loaded from the secret volume mounts.
	MountMtlsCerts *bool `json:"mountMtlsCerts,omitempty"`
	// Specifies the Configuration for Istio mesh across multiple clusters through the istio gateways.
	MultiCluster *MultiClusterConfig `json:"multiCluster,omitempty"`
	// Specifies whether native nftables rules should be used instead of iptables rules for traffic redirection.
	NativeNftables *bool `json:"nativeNftables,omitempty"`
	// Network defines the network this cluster belongs to. This name
	// corresponds to the networks in the map of mesh networks.
	Network string `json:"network,omitempty"`
	// Settings related to Kubernetes NetworkPolicy.
	NetworkPolicy *NetworkPolicyConfig `json:"networkPolicy,omitempty"`
	// Controls whether the creation of the sidecar injector ConfigMap should be skipped.
	// Defaults to false. When set to true, the sidecar injector ConfigMap will not be created.
	OmitSidecarInjectorConfigMap *bool `json:"omitSidecarInjectorConfigMap,omitempty"`
	// Controls whether the WebhookConfiguration resource(s) should be created. The current behavior
	// of Istiod is to manage its own webhook configurations.
	// When this option is set to true, Istio Operator, instead of webhooks, manages the
	// webhook configurations. When this option is set as false, webhooks manage their
	// own webhook configurations.
	OperatorManageWebhooks *bool `json:"operatorManageWebhooks,omitempty"`
	// Configure the Pilot certificate provider.
	// Currently, four providers are supported: "kubernetes", "istiod", "custom" and "none".
	PilotCertProvider string `json:"pilotCertProvider,omitempty"`
	// Specifies the k8s priorityClassName for the istio control plane components.
	//
	// See https://kubernetes.io/docs/concepts/configuration/pod-priority-preemption/#priorityclass
	//
	// Deprecated: use pod-level priority class configuration.
	PriorityClassName string `json:"priorityClassName,omitempty"`
	// Specifies how proxies are configured within Istio.
	Proxy *ProxyConfig `json:"proxy,omitempty"`
	// Specifies the Configuration for proxy_init container which sets the pods' networking
	// to intercept the inbound/outbound traffic.
	ProxyInit *ProxyInitConfig `json:"proxy_init,omitempty"`
	// Specifies the Istio control plane's pilot Pod IP address or remote cluster DNS resolvable hostname.
	RemotePilotAddress string `json:"remotePilotAddress,omitempty"`
	// Specifies resource scope for discovery selectors.
	ResourceScope ResourceScope `json:"resourceScope,omitempty"`
	// Specifies the Configuration for the SecretDiscoveryService instead of using
	// K8S secrets to mount the certificates.
	Sds *SDSConfig `json:"sds,omitempty"`
	// Specifies the configuration for Security Token Service.
	//
	// See https://tools.ietf.org/html/draft-ietf-oauth-token-exchange-16
	Sts *STSConfig `json:"sts,omitempty"`
	// Specifies the tag for the Istio docker images.
	Tag any `json:"tag,omitempty"`
	// The variant of the Istio container images to use. Options are "debug" or "distroless".
	// Unset will use the default for the given version.
	Variant string `json:"variant,omitempty"`
	// Specifies how waypoints are configured within Istio.
	Waypoint *WaypointConfig `json:"waypoint,omitempty"`
}

// ArchConfig specifies the pod scheduling target architecture(amd64, ppc64le, s390x, arm64)
// for all the Istio control plane components.
type ArchConfig struct {
	// Sets pod scheduling weight for amd64 arch.
	Amd64 *int `json:"amd64,omitempty"`
	// Sets pod scheduling weight for ppc64le arch.
	Ppc64le *int `json:"ppc64le,omitempty"`
	// Sets pod scheduling weight for s390x arch.
	S390x *int `json:"s390x,omitempty"`
	// Sets pod scheduling weight for arm64 arch.
	Arm64 *int `json:"arm64,omitempty"`
}

// Resources defines compute resources required by a container.
type Resources struct {
	// The maximum amount of compute resources allowed.
	//
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/
	Limits map[string]string `json:"limits,omitempty"`
	// The minimum amount of compute resources required. If Requests is omitted for a container,
	// it defaults to Limits if that is explicitly specified, otherwise to an implementation-defined value.
	//
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/
	Requests map[string]string `json:"requests,omitempty"`
}

// DefaultPodDisruptionBudgetConfig specifies the default pod disruption budget configuration.
//
// See https://kubernetes.io/docs/concepts/workloads/pods/disruptions/
type DefaultPodDisruptionBudgetConfig struct {
	// Controls whether a PodDisruptionBudget with a default minAvailable value of 1
	// is created for each deployment.
	Enabled *bool `json:"enabled,omitempty"`
}

// DefaultResourcesConfig specifies the default k8s resources settings for all
// Istio control plane components.
type DefaultResourcesConfig struct {
	// k8s resources settings.
	//
	// See https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container
	Requests *ResourcesRequestsConfig `json:"requests,omitempty"`
}

// ResourcesRequestsConfig is the configuration for K8s resource requests.
type ResourcesRequestsConfig struct {
	// CPU requests.
	Cpu string `json:"cpu,omitempty"`
	// Memory requests.
	Memory string `json:"memory,omitempty"`
}

// GlobalLoggingConfig specifies the global logging level settings for the Istio
// control plane components.
type GlobalLoggingConfig struct {
	// Comma-separated minimum per-scope logging level of messages to output, in the form of
	// <scope>:<level>,<scope>:<level>
	// The control plane has different scopes depending on component, but can configure
	// default log level across all components.
	// If empty, default scope and level will be used as configured in code.
	Level string `json:"level,omitempty"`
}

// IstiodConfig is the configuration for istiod.
type IstiodConfig struct {
	// If enabled, istiod will perform config analysis.
	EnableAnalysis *bool `json:"enableAnalysis,omitempty"`
}

// STSConfig is the configuration for Security Token Service (STS) server.
//
// See https://tools.ietf.org/html/draft-ietf-oauth-token-exchange-16
type STSConfig struct {
	ServicePort int `json:"servicePort,omitempty"`
}

// SDSConfig is the configuration for the SecretDiscoveryService instead of using
// K8S secrets to mount the certificates.
type SDSConfig struct {
	// Deprecated: no longer used.
	Token map[string]any `json:"token,omitempty"`
}

// ProxyConfig specifies how proxies are configured within Istio.
type ProxyConfig struct {
	// Controls the 'policy' in the sidecar injector.
	AutoInject string `json:"autoInject,omitempty"`
	// Domain for the cluster, default: "cluster.local".
	//
	// K8s allows this to be customized, see https://kubernetes.io/docs/tasks/administer-cluster/dns-custom-nameservers/
	ClusterDomain string `json:"clusterDomain,omitempty"`
	// Per Component log level for proxy, applies to gateways and sidecars.
	//
	// If a component level is not set, then the global "logLevel" will be used. If left empty, "misc:error" is used.
	ComponentLogLevel string `json:"componentLogLevel,omitempty"`
	// Enables core dumps for newly injected sidecars.
	//
	// If set, newly injected sidecars will have core dumps enabled.
	//
	// Deprecated: no longer recommended.
	EnableCoreDump *bool `json:"enableCoreDump,omitempty"`
	// Specifies the Istio ingress ports not to capture.
	ExcludeInboundPorts string `json:"excludeInboundPorts,omitempty"`
	// Lists the excluded IP ranges of Istio egress traffic that the sidecar captures.
	ExcludeIPRanges string `json:"excludeIPRanges,omitempty"`
	// A comma separated list of outbound ports to be excluded from redirection to Envoy.
	ExcludeOutboundPorts string `json:"excludeOutboundPorts,omitempty"`
	// Image name or path for the proxy, default: "proxyv2".
	//
	// If registry or tag are not specified, global.hub and global.tag are used.
	//
	// Examples: my-proxy (uses global.hub/tag), docker.io/myrepo/my-proxy:v1.0.0
	Image string `json:"image,omitempty"`
	// Lists the IP ranges of Istio egress traffic that the sidecar captures.
	//
	// Example: "172.30.0.0/16,172.20.0.0/16"
	// This would only capture egress traffic on those two IP Ranges, all other outbound traffic would
	// be allowed by the sidecar.
	IncludeIPRanges string `json:"includeIPRanges,omitempty"`
	// A comma separated list of inbound ports for which traffic is to be redirected to Envoy.
	// The wildcard character '*' can be used to configure redirection for all ports.
	IncludeInboundPorts string `json:"includeInboundPorts,omitempty"`
	// A comma separated list of outbound ports for which traffic is to be redirected to Envoy,
	// regardless of the destination IP.
	IncludeOutboundPorts string `json:"includeOutboundPorts,omitempty"`
	// The k8s lifecycle hooks definition (pod.spec.containers.lifecycle) for the proxy container.
	//
	// More info: https://kubernetes.io/docs/concepts/containers/container-lifecycle-hooks/#container-hooks
	Lifecycle *corev1.Lifecycle `json:"lifecycle,omitempty"`
	// Log level for proxy, applies to gateways and sidecars. If left empty, "warning" is used.
	// Expected values are: trace|debug|info|warning|error|critical|off
	LogLevel string `json:"logLevel,omitempty"`
	// Path to the file to which the proxy will write outlier detection logs.
	//
	// Example: "/dev/stdout"
	// This would write the logs to standard output.
	OutlierLogPath string `json:"outlierLogPath,omitempty"`
	// Enables privileged securityContext for the istio-proxy container.
	//
	// See https://kubernetes.io/docs/tasks/configure-pod-container/security-context/
	Privileged *bool `json:"privileged,omitempty"`
	// Sets the initial delay for readiness probes in seconds.
	ReadinessInitialDelaySeconds int `json:"readinessInitialDelaySeconds,omitempty"`
	// Sets the interval between readiness probes in seconds.
	ReadinessPeriodSeconds int `json:"readinessPeriodSeconds,omitempty"`
	// Sets the number of successive failed probes before indicating readiness failure.
	ReadinessFailureThreshold int `json:"readinessFailureThreshold,omitempty"`
	// K8s resources settings.
	//
	// See https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container
	//
	// Deprecated: use pod-level resource configuration.
	Resources *Resources `json:"resources,omitempty"`
	// Configures the seccomp profile for the istio-validation and istio-proxy containers.
	//
	// See: https://kubernetes.io/docs/tutorials/security/seccomp/
	SeccompProfile *corev1.SeccompProfile `json:"seccompProfile,omitempty"`
	// Configures the startup probe for the istio-proxy container.
	StartupProbe *StartupProbe `json:"startupProbe,omitempty"`
	// Default port used for the Pilot agent's health checks.
	StatusPort int `json:"statusPort,omitempty"`
	// Specify which tracer to use. One of: zipkin, lightstep, datadog, stackdriver.
	// If using stackdriver tracer outside GCP, set env GOOGLE_APPLICATION_CREDENTIALS to the GCP credential file.
	Tracer string `json:"tracer,omitempty"`
	// Controls if sidecar is injected at the front of the container list and blocks the start of the
	// other containers until the proxy is ready.
	//
	// Deprecated: replaced by ProxyConfig setting which allows per-pod configuration of this behavior.
	HoldApplicationUntilProxyStarts *bool `json:"holdApplicationUntilProxyStarts,omitempty"`
}

// StartupProbe configures the startup probe for the istio-proxy container.
type StartupProbe struct {
	// Enables or disables a startup probe.
	// For optimal startup times, changing this should be tied to the readiness probe values.
	//
	// If the probe is enabled, it is recommended to have delay=0s,period=15s,failureThreshold=4.
	// This ensures the pod is marked ready immediately after the startup probe passes (which has a 1s poll interval),
	// and doesn't spam the readiness endpoint too much
	//
	// If the probe is disabled, it is recommended to have delay=1s,period=2s,failureThreshold=30.
	// This ensures the startup is reasonable fast (polling every 2s). 1s delay is used since the startup is not often ready instantly.
	Enabled *bool `json:"enabled,omitempty"`
	// Minimum consecutive failures for the probe to be considered failed after having succeeded.
	FailureThreshold int `json:"failureThreshold,omitempty"`
}

// ProxyInitConfig specifies the configuration for proxy_init container which sets the pods'
// networking to intercept the inbound/outbound traffic.
type ProxyInitConfig struct {
	// Specifies the image for the proxy_init container.
	Image string `json:"image,omitempty"`
	// K8s resources settings.
	//
	// See https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container
	//
	// Deprecated: use pod-level resource configuration.
	Resources *Resources `json:"resources,omitempty"`
	// Forces iptables to be applied even if the container is not running as root.
	ForceApplyIptables *bool `json:"forceApplyIptables,omitempty"`
}

// MultiClusterConfig specifies the Configuration for Istio mesh across multiple clusters
// through the istio gateways.
type MultiClusterConfig struct {
	// Enables the connection between two kubernetes clusters via their respective
	// ingressgateway services. Use if the pods in each cluster cannot directly
	// talk to one another.
	Enabled *bool `json:"enabled,omitempty"`
	// The name of the cluster this installation will run in. This is required for
	// sidecar injection to properly label proxies.
	ClusterName string `json:"clusterName,omitempty"`
	// The suffix for global service names.
	GlobalDomainSuffix string `json:"globalDomainSuffix,omitempty"`
	// Enable envoy filter to translate `globalDomainSuffix` to cluster local suffix
	// for cross cluster communication.
	IncludeEnvoyFilter *bool `json:"includeEnvoyFilter,omitempty"`
}

// WaypointConfig specifies the configuration for Waypoint proxies.
type WaypointConfig struct {
	// K8s resource settings.
	//
	// See https://kubernetes.io/docs/concepts/configuration/manage-compute-resources-container/#resource-requests-and-limits-of-pod-and-container
	Resources *Resources `json:"resources,omitempty"`
	// K8s affinity settings for waypoint pods.
	//
	// See https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/#inter-pod-affinity-and-anti-affinity
	Affinity *corev1.Affinity `json:"affinity,omitempty"`
	// K8s topology spread constraints settings.
	//
	// See https://kubernetes.io/docs/concepts/workloads/pods/pod-topology-spread-constraints/
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
	// K8s node labels settings.
	//
	// See https://kubernetes.io/docs/user-guide/node-selection/
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// K8s tolerations settings.
	//
	// See https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/
	Toleration []corev1.Toleration `json:"tolerations,omitempty"`
}

// NetworkPolicyConfig specifies the configuration for Kubernetes NetworkPolicy.
type NetworkPolicyConfig struct {
	// Controls whether default NetworkPolicy resources will be created.
	Enabled *bool `json:"enabled,omitempty"`
}

// ImagePullPolicy specifies when the kubelet should pull images.
type ImagePullPolicy string

const (
	ImagePullPolicyAlways       ImagePullPolicy = "Always"
	ImagePullPolicyBlank        ImagePullPolicy = ""
	ImagePullPolicyIfNotPresent ImagePullPolicy = "IfNotPresent"
	ImagePullPolicyNever        ImagePullPolicy = "Never"
)

// ResourceScope specifies the resource scope for discovery selectors.
type ResourceScope string

const (
	ResourceScopeAll       ResourceScope = "all"
	ResourceScopeCluster   ResourceScope = "cluster"
	ResourceScopeNamespace ResourceScope = "namespace"
)
