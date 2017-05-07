package engine

import (
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestModelTrafficScanning(t *testing.T) {
	const (
		unknown  = pb.GuardrailEffectivePolicy_GUARDRAIL_EFFECTIVE_POLICY_UNSPECIFIED
		none     = pb.GuardrailEffectivePolicy_GUARDRAIL_EFFECTIVE_POLICY_NONE
		baseline = pb.GuardrailEffectivePolicy_GUARDRAIL_EFFECTIVE_POLICY_BASELINE
		admin    = pb.GuardrailEffectivePolicy_GUARDRAIL_EFFECTIVE_POLICY_ADMIN
	)
	cases := []struct {
		name      string
		cred      Kind
		effective pb.GuardrailEffectivePolicy
		want      pb.CodeModelTrafficScanning
	}{
		{"tenant key never scanned, even with an admin policy", KindSecret, admin, pb.CodeModelTrafficScanning_CODE_MODEL_TRAFFIC_SCANNING_UNSCANNED_TENANT_KEY},
