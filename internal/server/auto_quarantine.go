package server

import (
	"context"
	"log"
	"strings"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Auto-quarantine (#659): the honest realization of the "scanner → virtual-patch"
// capstone. When the ClamAV scanner finds malware in a container, block that
// tenant's egress (a deny-all virtual-patch rule) so a compromised container
// can't exfiltrate or call out.
//
// A "clean" verdict never releases it (#2631): the verdict is produced by the
// scan running inside the tenant's own box, so it is self-reported, and a
// compromised box could otherwise lift its own quarantine. Release is by an
// operator removing the rule (note quarantineNote) or by the TTL below.
//
// This is the one scanner finding that maps CLEANLY onto the Tier 1 deny rules:
// an "infected" malware verdict ↔ a network-quarantine action. (Trivy
// package CVEs, by contrast, describe a vulnerability INSIDE the container with
// no network endpoint to block, so they are deliberately NOT wired here — see
// docs/security/AUTO-QUARANTINE.md.)
//
// Caveats (documented): deny rules are per-TENANT, so quarantining one infected
// container blocks egress for all that tenant's containers — containment over
// availability, acceptable for a malware response and opt-in. Enforcement is
// only as strong as the network-policy BPF enforcer that's enabled
// (CONTAINARIUM_NETWORK_POLICY_BPF_OBJECT + _ENFORCE); without it the rule is
// stored but not dropped.

// quarantineCIDR is the deny target for a full egress quarantine (block all
// outbound). Deny beats allow, so this blackholes the tenant's egress.
const quarantineCIDR = "0.0.0.0/0"

// quarantineNote marks a deny rule as auto-added by quarantine, so an operator can
