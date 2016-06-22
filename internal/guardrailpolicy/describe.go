package guardrailpolicy

import (
	"fmt"
	"strings"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Describe renders a GetGuardrailPolicyResponse for a person: the text the
// `guardrail policy get` CLI prints and the MCP tool returns. Signers are
// shown by key id and label, never by key bytes.
func Describe(resp *pb.GetGuardrailPolicyResponse) string {
	if !resp.GetConfigured() {
		return "Guardrail policy: not configured (no server policy has been set; ungated deploys are unaffected)\n"
	}
	p := resp.GetPolicy()
