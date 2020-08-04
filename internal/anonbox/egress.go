package anonbox

import (
	"fmt"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// EgressACLName is the one shared Incus network ACL every anonymous box's
// NIC carries. Shared on purpose: the rules are identical for every box,
// and one ACL means one place an operator reads to see what the tier may
// reach.
const EgressACLName = "containarium-anon-egress"

// egressACL is "deny-by-default except DNS, HTTP, HTTPS" (PRD story 4).
// The deny itself is not a rule here — it is the NIC's
// security.acls.default.egress.action=drop (see nicACLKeys) — so the ACL
// only lists what is allowed. Ingress is left to the NIC default (allow):
// the sentinel must still reach sshd.
//
// Why an Incus ACL and not a NetworkPolicy (decision on #2197): the
// NetworkPolicy model allows by CIDR/domain only and hooks container
// veths, so it can neither express a port allow-list nor see a VM's NIC.
func egressACL() incus.ACLConfig {
	allow := func(proto, port string) incus.ACLRule {
		return incus.ACLRule{
			Action:          "allow",
			Destination:     "",
			DestinationPort: port,
			Protocol:        proto,
			Description:     fmt.Sprintf("anon tier: %s/%s", proto, port),
		}
	}
	return incus.ACLConfig{
		Name:        EgressACLName,
		Description: "anonymous-box egress: DNS, HTTP, HTTPS only; everything else dropped by the NIC default",
		EgressRules: []incus.ACLRule{
			allow("udp", "53"),
			allow("tcp", "53"),
			allow("tcp", "80"),
			allow("tcp", "443"),
		},
	}
}

// nicACLKeys are the per-device keys that attach the ACL and make the
// NIC drop any egress the ACL does not allow. Dropped egress is logged so
// an abuse investigation has evidence (#2200).
func nicACLKeys() map[string]string {
	return map[string]string{
		"security.acls":                        EgressACLName,
		"security.acls.default.egress.action":  "drop",
		"security.acls.default.egress.logged":  "true",
		"security.acls.default.ingress.action": "allow",
	}
}

// applyEgressGuard makes the ACL exist, then attaches it to the box's NIC.
// Same shape as the core-infra guard's reconciler: an ACL that could not
// be written is never attached.
func (m *Manager) applyEgressGuard(boxName string) error {
	desired := egressACL()
	if _, err := m.acls.GetNetworkACL(desired.Name); err != nil {
