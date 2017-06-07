package config

// CONTAINARIUM_NETWORK_* variable names — the single source of truth for the
// in-kernel network-policy (eBPF) namespace.
const (
	EnvNetworkPolicyBPFObject  = "CONTAINARIUM_NETWORK_POLICY_BPF_OBJECT"
	EnvNetworkPolicyEnforce    = "CONTAINARIUM_NETWORK_POLICY_ENFORCE"
	EnvNetworkPolicySignatures = "CONTAINARIUM_NETWORK_POLICY_SIGNATURES"
	// EnvCoreGuard arms the core-infra network guard: Incus NIC ACLs that
	// keep tenant containers off the platform's core-role containers
	// (docs/architecture/core-infra-network-guard.md). "enforce" attaches
	// them; anything else is off.
	EnvCoreGuard = "CONTAINARIUM_CORE_GUARD"
	// EnvTenantGuard arms the tenant network guard: Incus NIC ACLs that keep
	// one tenant's containers off another tenant's containers
	// (docs/architecture/tenant-network-guard.md). Unset is enforce; only
	// "off" disables (nicguard.ParseMode).
	EnvTenantGuard = "CONTAINARIUM_TENANT_GUARD"
	// EnvDefaultEgress selects the egress posture of a tenant with no stored
	// network policy (#2577, stage 1 of the staged default-deny decision).
	// Unset is the default allow-list in log_only; only "off" restores the
	// bare observe-only fallback. It never arms enforcement.
	EnvDefaultEgress = "CONTAINARIUM_NETWORK_DEFAULT_EGRESS"
	// EnvDefaultEgressDomains / EnvDefaultEgressCIDRs are comma-separated
	// additions to that default allow-list: the model gateway and the control
	// plane, which are deployment-specific.
	EnvDefaultEgressDomains = "CONTAINARIUM_NETWORK_DEFAULT_EGRESS_DOMAINS"
	EnvDefaultEgressCIDRs   = "CONTAINARIUM_NETWORK_DEFAULT_EGRESS_CIDRs"
	// EnvNativeAccountTenancy arms automatic per-user account network
	// namespaces: with it set, a non-admin local-LXC create that names no
	// network Tenant is tenanted under "account:<username>". Unset (the
	// default) keeps omitted-field creates on the legacy un-tenanted path,
	// so the tenancy precondition can never block an operator who did not
	// ask for it. The shared-Tenant admin surface is unaffected either way.
	EnvNativeAccountTenancy = "CONTAINARIUM_NATIVE_ACCOUNT_TENANCY"
)

// Network is the typed view of the CONTAINARIUM_NETWORK_* namespace — the eBPF
// network-policy enforcer's startup wiring. Both arming flags are off unless
// explicitly set, matching the deny→audit (observation-only) default posture.
type Network struct {
	// PolicyBPFObject is the path to the loaded in-kernel network-policy program
	// object. Empty disables the enforcer entirely. Kept as the raw value;
	// consumers TrimSpace where they need to. (EnvNetworkPolicyBPFObject)
	PolicyBPFObject string

