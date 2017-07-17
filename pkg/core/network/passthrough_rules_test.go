package network

import (
	"strings"
	"testing"
)

// A passthrough route must be reachable from the daemon host itself,
// not only from other machines. DNAT in PREROUTING never sees
// locally-generated packets — those traverse OUTPUT — so a published
// cluster endpoint answered "connection refused" to anything running on
// the host, including the e2e lane and any operator curling it (#1459).
//
// PortForwarder has always added both chains for exactly this reason;
// the passthrough path added only PREROUTING.
func TestPassthroughRuleArgs(t *testing.T) {
	pre := passthroughPreRoutingArgs(36443, "10.166.0.9", 6443, "tcp", "10.166.0.0/24")
	joined := strings.Join(pre, " ")
	if !strings.Contains(joined, "PREROUTING") || !strings.Contains(joined, "DNAT") {
		t.Fatalf("prerouting args are not a DNAT rule: %v", pre)
	}
	// Container-network traffic stays excluded, so containers keep
	// reaching external services on the same port.
	if !strings.Contains(joined, "10.166.0.0/24") {
		t.Errorf("prerouting rule lost its container-network exclusion: %v", pre)
	}

	out := passthroughOutputArgs(36443, "10.166.0.9", 6443, "tcp")
	oj := strings.Join(out, " ")
	if !strings.Contains(oj, "OUTPUT") || !strings.Contains(oj, "DNAT") {
