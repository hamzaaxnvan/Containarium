package engine

import (
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// ModelTrafficScanning says whether a run's model responses are scanned by
// the inbound guardrail (#2367). A tenant-secret run talks to the provider
// directly, so its traffic never crosses the gateway and is never scanned.
// A gateway run follows effective, the daemon's own answer to `guardrail
// status` (#2737): ADMIN is SCANNED, BASELINE (no policy ever set) is the
// built-in baseline scan (#2851), and NONE (a policy with no inbound BLOCK
// rule) is unscanned. UNSPECIFIED means the policy could not be read, which is
// reported as unknown rather than a guess in either direction.
func ModelTrafficScanning(cred Kind, effective pb.GuardrailEffectivePolicy) pb.CodeModelTrafficScanning {
	switch cred {
	case KindSecret:
		return pb.CodeModelTrafficScanning_CODE_MODEL_TRAFFIC_SCANNING_UNSCANNED_TENANT_KEY
	case KindGateway:
		switch effective {
		case pb.GuardrailEffectivePolicy_GUARDRAIL_EFFECTIVE_POLICY_ADMIN:
			return pb.CodeModelTrafficScanning_CODE_MODEL_TRAFFIC_SCANNING_SCANNED
		case pb.GuardrailEffectivePolicy_GUARDRAIL_EFFECTIVE_POLICY_BASELINE:
			return pb.CodeModelTrafficScanning_CODE_MODEL_TRAFFIC_SCANNING_BASELINE
		case pb.GuardrailEffectivePolicy_GUARDRAIL_EFFECTIVE_POLICY_NONE:
			return pb.CodeModelTrafficScanning_CODE_MODEL_TRAFFIC_SCANNING_UNSCANNED_NO_POLICY
		}
	}
	return pb.CodeModelTrafficScanning_CODE_MODEL_TRAFFIC_SCANNING_UNSPECIFIED
}

// ScanningLine renders the one-line status `code run` prints, shared by the
// CLI and the MCP tool so the two cannot drift.
func ScanningLine(s pb.CodeModelTrafficScanning) string {
	switch s {
