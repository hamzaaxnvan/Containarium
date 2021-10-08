package guardrail

import (
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// IsInboundKind reports whether k is scanned on model output (as opposed to
// the outbound PII and secret kinds).
func IsInboundKind(k pb.GuardrailKind) bool {
	return k == pb.GuardrailKind_GUARDRAIL_KIND_UNSAFE_CODE || k == pb.GuardrailKind_GUARDRAIL_KIND_PROMPT_INJECTION
}

// InboundBlockKinds returns the inbound kinds the server policy blocks, each
// once, in rule order. Empty means no inbound scan is in force.
func InboundBlockKinds(p *pb.ServerGuardrailPolicy) []pb.GuardrailKind {
	var kinds []pb.GuardrailKind
	seen := map[pb.GuardrailKind]bool{}
	for _, r := range p.GetPolicy().GetRules() {
		k := r.GetKind()
		if !IsInboundKind(k) || r.GetAction() != pb.GuardrailAction_GUARDRAIL_ACTION_BLOCK || seen[k] {
			continue
		}
		seen[k] = true
		kinds = append(kinds, k)
	}
	return kinds
}

// InboundReason is why an inbound scan (model output on its way into a
// box) passed or blocked. Typed, not a string: an audit entry and a counter
// key on it (docs/architecture/guardrail-inbound-and-server-policy.md).
type InboundReason int

const (
	InboundReasonClean             InboundReason = iota
	InboundReasonFinding                         // a BLOCK-action kind matched
	InboundReasonCoverageGap                     // engine could not scan a unit
	InboundReasonEngineError                     // unreachable, timeout, or unsupported kind
	InboundReasonOverLimit                       // held message exceeded the byte limit
	InboundReasonPolicyUnavailable               // policy store could not be read
)

var inboundReasonNames = map[InboundReason]string{
	InboundReasonClean:             "clean",
	InboundReasonFinding:           "finding",
	InboundReasonCoverageGap:       "coverage_gap",
	InboundReasonEngineError:       "engine_error",
	InboundReasonOverLimit:         "over_limit",
	InboundReasonPolicyUnavailable: "policy_unavailable",
