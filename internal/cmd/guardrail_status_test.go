package cmd

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fixedGuardrailStatusAPI answers GetGuardrailStatus with resp or err.
type fixedGuardrailStatusAPI struct {
	resp  *pb.GetGuardrailStatusResponse
	err   error
	calls int
}

func (f *fixedGuardrailStatusAPI) GetGuardrailStatus() (*pb.GetGuardrailStatusResponse, error) {
	f.calls++
	return f.resp, f.err
}

func withFixedGuardrailStatusAPI(t *testing.T, f *fixedGuardrailStatusAPI) {
	t.Helper()
	prev := newGuardrailStatusAPI
	newGuardrailStatusAPI = func() (guardrailStatusAPI, func(), error) { return f, func() {}, nil }
	t.Cleanup(func() { newGuardrailStatusAPI = prev })
}

func runGuardrailStatusCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	guardrailStatusJSON = false
	return runGuardrail(t, append([]string{"status"}, args...)...)
}

// TestGuardrailStatusCLI_PrintsEachEffectivePolicy (#2737): the command
// prints the daemon's effective response policy as the word the Verify
// steps look for (none / baseline / admin).
func TestGuardrailStatusCLI_PrintsEachEffectivePolicy(t *testing.T) {
	cases := []struct {
		name string
		resp *pb.GetGuardrailStatusResponse
		want []string
	}{
		{"baseline", &pb.GetGuardrailStatusResponse{
			ResponsePolicy:  pb.GuardrailEffectivePolicy_GUARDRAIL_EFFECTIVE_POLICY_BASELINE,
			BaselineRuleIds: []string{"baseline.pem_private_key"},
		}, []string{"Response policy: baseline", "baseline.pem_private_key"}},
		{"admin", &pb.GetGuardrailStatusResponse{
			ResponsePolicy:    pb.GuardrailEffectivePolicy_GUARDRAIL_EFFECTIVE_POLICY_ADMIN,
			PolicyRevision:    3,
			InboundBlockKinds: []pb.GuardrailKind{pb.GuardrailKind_GUARDRAIL_KIND_UNSAFE_CODE},
		}, []string{"Response policy: admin", "revision 3", "UNSAFE_CODE"}},
		{"none", &pb.GetGuardrailStatusResponse{
			ResponsePolicy: pb.GuardrailEffectivePolicy_GUARDRAIL_EFFECTIVE_POLICY_NONE,
			PolicyRevision: 4,
		}, []string{"Response policy: none", "revision 4"}},
		// #2738: the switch shows in the status.
		{"baseline block", &pb.GetGuardrailStatusResponse{
			ResponsePolicy:  pb.GuardrailEffectivePolicy_GUARDRAIL_EFFECTIVE_POLICY_BASELINE,
			BaselineRuleIds: []string{"baseline.pem_private_key"},
			Baseline:        &pb.GuardrailBaselineSetting{Mode: pb.GuardrailBaselineMode_GUARDRAIL_BASELINE_MODE_BLOCK, Revision: 1, UpdatedBy: "ops"},
		}, []string{"Response policy: baseline", "Baseline mode: block (revision 1, set by ops)", "blocked on a"}},
		{"baseline default log", &pb.GetGuardrailStatusResponse{
			ResponsePolicy:  pb.GuardrailEffectivePolicy_GUARDRAIL_EFFECTIVE_POLICY_BASELINE,
			BaselineRuleIds: []string{"baseline.pem_private_key"},
			Baseline:        &pb.GuardrailBaselineSetting{Mode: pb.GuardrailBaselineMode_GUARDRAIL_BASELINE_MODE_LOG},
		}, []string{"Baseline mode: log (default, never set)", "log-only"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fixedGuardrailStatusAPI{resp: tc.resp}
			withFixedGuardrailStatusAPI(t, f)
			out, err := runGuardrailStatusCLI(t)
			if err != nil {
				t.Fatalf("guardrail status: %v (%s)", err, out)
			}
			if f.calls != 1 {
				t.Fatalf("GetGuardrailStatus called %d times, want 1", f.calls)
