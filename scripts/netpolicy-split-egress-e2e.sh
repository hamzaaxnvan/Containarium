#!/usr/bin/env bash
#
# netpolicy-split-egress-e2e.sh — proves the split egress mode (#2970 / #2974)
# end to end with the daemon's enforce switch ON (#2981): a real daemon, the
# real eBPF object, two real tenant containers on one Incus bridge, real
# packets, and the audit rows the daemon writes from each deny event's
# verdict byte.
#
# A tenant's policy has two modes. `mode` governs the cross-tenant,
# intra-tenant and metadata checks; `egress_mode` governs only a miss on the
# egress allow-list (experimental/ebpf-phaseA/netpolicy.bpf.c, netpolicy_ingress:
# `deny_mode = cfg->egress_mode` on an allow-list miss). The scenario:
#
#   phase 1  A: mode=ENFORCE, egress_mode=LOG_ONLY
#            A -> B             dropped, audited network_policy.deny_dropped
#            A -> unlisted host passes,  audited network_policy.deny_logged
#   phase 2  A: mode=LOG_ONLY, egress_mode=ENFORCE (the reverse split)
#            A -> unlisted host dropped, audited network_policy.deny_dropped
#            A -> B             follows mode: passes, audited deny_logged
#   both     B (no stored policy) keeps its reachability and is never dropped.
#
# Why the audit action proves the verdict byte: OnDenyEvent labels a row from
# the verdict the program reported, and falls back to the tenant-wide `mode`
# only for an event without one (network_policy_enforcer.go,
# denyEventDropped). In both split phases that fallback gives the OPPOSITE
# label for the allow-list miss, so `deny_logged` in phase 1 and
# `deny_dropped` in phase 2 can only come from the verdict byte.
#
# The "unlisted host" is the bridge gateway (the runner host itself): it is
# not a managed container (so it reaches the allow-list branch, not the
# cross-tenant one), A's policy lists no egress CIDR and no preset (so the
# gateway is not implicitly allowed), and it answers ICMP without any
# internet dependency. ICMP is used throughout so neither box needs a
# listener installed.
#
# The enforcer is sender-side with no connection tracking: a reply is policed
# as the replier's own egress. So "B is unaffected" is asserted on B's own
# egress and on B's audit verdicts, not on B -> A (A's echo replies are A -> B
# traffic and follow A's `mode`).
#
# Preconditions (the workflow sets them; the script checks what it can):
#   - the daemon runs with CONTAINARIUM_NETWORK_POLICY_BPF_OBJECT set,
#     CONTAINARIUM_NETWORK_POLICY_ENFORCE=1 and --postgres;
#   - CONTAINARIUM_TENANT_GUARD=off, so the eBPF enforcer is the only filter
#     between the two boxes (the guard's Incus NIC ACL drops cross-tenant
#     ingress regardless of policy, which would hide phase 2's "A -> B
#     follows mode"; the guard has its own lane, tenant-guard-e2e.yml);
#   - containers <tenant-a>-container and <tenant-b>-container exist, owned
#     by the two tenants, and B has no stored network policy.
#
# Usage (on the lane host, with incus access via sudo):
#   scripts/netpolicy-split-egress-e2e.sh <tenant-a> <tenant-b>
