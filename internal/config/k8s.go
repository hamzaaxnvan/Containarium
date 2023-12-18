package config

import (
	"fmt"
)

// CONTAINARIUM_K8S_* variable names — the single source of truth for the
// Kubernetes-backend namespace. Reference these instead of string literals.
const (
	EnvK8sKubeconfig               = "CONTAINARIUM_K8S_KUBECONFIG"
	EnvK8sGatewayNamespace         = "CONTAINARIUM_K8S_GATEWAY_NAMESPACE"
	EnvK8sGatewayHost              = "CONTAINARIUM_K8S_GATEWAY_HOST"
	EnvK8sGatewaySSHPort           = "CONTAINARIUM_K8S_GATEWAY_SSH_PORT"
	EnvK8sTenantNSPrefix           = "CONTAINARIUM_K8S_TENANT_NS_PREFIX"
	EnvK8sBoxImage                 = "CONTAINARIUM_K8S_BOX_IMAGE"
	EnvK8sBoxMode                  = "CONTAINARIUM_K8S_BOX_MODE"
	EnvK8sStorageClass             = "CONTAINARIUM_K8S_STORAGE_CLASS"
	EnvK8sSharedVolumeStorageClass = "CONTAINARIUM_K8S_SHARED_VOLUME_STORAGE_CLASS"
	EnvK8sGatewayUpstreamPublicKey = "CONTAINARIUM_K8S_GATEWAY_UPSTREAM_PUBLIC_KEY"
	// #nosec G101 -- this is the NAME of an environment variable, not a credential value.
	EnvK8sGatewayUpstreamKeySecret = "CONTAINARIUM_K8S_GATEWAY_UPSTREAM_KEY_SECRET"
	EnvK8sInsecureIgnoreHostKey    = "CONTAINARIUM_K8S_INSECURE_IGNORE_HOST_KEY"
	EnvK8sGatewayService           = "CONTAINARIUM_K8S_GATEWAY_SERVICE"
	EnvK8sGatewayAdvertisePort     = "CONTAINARIUM_K8S_GATEWAY_ADVERTISE_PORT"
	EnvK8sDefaultMemoryRequest     = "CONTAINARIUM_K8S_DEFAULT_MEMORY_REQUEST"
	EnvK8sDefaultMemoryLimit       = "CONTAINARIUM_K8S_DEFAULT_MEMORY_LIMIT"
	EnvK8sDisableMemoryFloor       = "CONTAINARIUM_K8S_DISABLE_MEMORY_FLOOR"
	EnvK8sOperator                 = "CONTAINARIUM_K8S_OPERATOR"
	EnvK8sBoxNamespace             = "CONTAINARIUM_K8S_BOX_NAMESPACE"
	EnvK8sRuntimeClass             = "CONTAINARIUM_K8S_RUNTIME_CLASS"
)

// K8s defaults applied by LoadK8s when the variable is unset.
const (
	defaultK8sGatewayNamespace = "agent-gateway"
	defaultK8sTenantNSPrefix   = "tenant-"
	defaultK8sGatewaySSHPort   = 22
	defaultK8sGatewayService   = "sshpiper"
	defaultK8sBoxNamespace     = "default"
)

// K8s is the typed view of the CONTAINARIUM_K8S_* namespace — the wiring the
// daemon needs to drive the Kubernetes agent-box backend. Load it once at
// startup with LoadK8s; the server maps it onto pkg/core/box/k8s.Config (kept
// env-agnostic so that package stays free of this internal config dependency).
type K8s struct {
	// Kubeconfig is the path to a kubeconfig file; empty means in-cluster config
	// then the ambient loading rules. (EnvK8sKubeconfig)
	Kubeconfig string

	// GatewayNamespace is where the sshpiper Deployment + its LB Service live.
	// (EnvK8sGatewayNamespace; default "agent-gateway")
	GatewayNamespace string

	// GatewayHost is the public SSH endpoint agents connect to (the sshpiper LB).
	// (EnvK8sGatewayHost)
	GatewayHost string

	// GatewaySSHPort is the gateway SSH port. (EnvK8sGatewaySSHPort; default 22)
	GatewaySSHPort int

	// TenantNamespacePrefix prefixes each per-tenant namespace.
	// (EnvK8sTenantNSPrefix; default "tenant-")
