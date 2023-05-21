#!/usr/bin/env bash
#
# tenant-tenant-network-isolation-e2e.sh — proves (or disproves) the
# tenant-to-tenant boundary on a shared backend: can one tenant's container
# reach another tenant's container over the bridge (incusbr0), at the network
# layer, with no application credential involved?
#
# Companion to tenant-core-infra-network-isolation-e2e.sh (tenant -> core
# infra). That script asks whether a tenant can reach the platform; this one
# asks whether a tenant can reach a NEIGHBOUR. See
# docs/security/multi-tenant-isolation.md, "Tenant <-> tenant".
#
# THIS TEST IS EXPECTED TO FAIL (red) on a default-configured backend. The
# only mechanism that can drop tenant->tenant traffic is the eBPF
# NetworkPolicy enforcer (#315), and it drops only when ALL of these hold:
#   1. the daemon runs with CONTAINARIUM_NETWORK_POLICY_BPF_OBJECT set,
#   2. the daemon runs with CONTAINARIUM_NETWORK_POLICY_ENFORCE=1,
#   3. the SENDING tenant has a stored policy with --mode enforce.
# A tenant with no policy is compiled as log_only (never dropped), the
# enforcer is sender-side only, and no Incus NIC key (security.port_isolation,
# security.mac_filtering, security.ipv4_filtering) or ACL is applied to tenant
# NICs. Turning this green is the acceptance criterion for the fix.
#
# Usage (run ON the backend host, as an operator with incus access — this is
# something an operator verifies FOR tenants, not something a tenant runs):
#   sudo bash scripts/tenant-tenant-network-isolation-e2e.sh <tenant-a> <tenant-b>
#
# <tenant-a> and <tenant-b> must be two already-existing, ordinary containers
# owned by DIFFERENT tenants (different user.containarium.tenant /
# cloud_org_id labels, or different <tenant>-container prefixes). Use fixture
# tenants, never real customers. The script never creates or modifies either
# container; it only uses `incus exec` in A's network namespace to attempt
# bare connections toward B's bridge address (and B toward A, because the
# enforcer is sender-side).
#
