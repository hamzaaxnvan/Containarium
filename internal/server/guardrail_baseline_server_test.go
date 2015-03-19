package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/guardrailpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

const (
	gpModeLog   = pb.GuardrailBaselineMode_GUARDRAIL_BASELINE_MODE_LOG
	gpModeBlock = pb.GuardrailBaselineMode_GUARDRAIL_BASELINE_MODE_BLOCK
)

func gpSetMode(ctx context.Context, s *GuardrailPolicyServer, mode pb.GuardrailBaselineMode) (*pb.SetGuardrailBaselineModeResponse, error) {
	return s.SetGuardrailBaselineMode(ctx, &pb.SetGuardrailBaselineModeRequest{Mode: mode})
}

// TestSetBaselineMode_AdminOnly (#2738): only an admin may switch the
// baseline mode; a refused call changes nothing and writes no audit entry.
func TestSetBaselineMode_AdminOnly(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
		mode pb.GuardrailBaselineMode
		want codes.Code
	}{
		{"admin", gpAdminCtx(), gpModeBlock, codes.OK},
		{"non-admin", gpUserCtx(), gpModeBlock, codes.PermissionDenied},
		{"unauthenticated", context.Background(), gpModeBlock, codes.Unauthenticated},
		{"unspecified mode", gpAdminCtx(), pb.GuardrailBaselineMode_GUARDRAIL_BASELINE_MODE_UNSPECIFIED, codes.InvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, audit := newGPServer()
			changed := 0
			s.SetOnChange(func() { changed++ })
			_, err := gpSetMode(tc.ctx, s, tc.mode)
			if got := status.Code(err); got != tc.want {
				t.Fatalf("SetGuardrailBaselineMode as %s = %v (%v), want %v", tc.name, got, err, tc.want)
			}
			st, err := s.GetGuardrailStatus(gpUserCtx(), &pb.GetGuardrailStatusRequest{})
			if err != nil {
				t.Fatalf("GetGuardrailStatus: %v", err)
			}
			ok := tc.want == codes.OK
			wantMode, wantRev, wantEntries, wantChanged := gpModeLog, int64(0), 0, 0
			if ok {
				wantMode, wantRev, wantEntries, wantChanged = gpModeBlock, 1, 1, 1
			}
			if st.GetBaseline().GetMode() != wantMode || st.GetBaseline().GetRevision() != wantRev {
				t.Fatalf("baseline after a %s set = %v, want %v at revision %d", tc.name, st.GetBaseline(), wantMode, wantRev)
			}
			if n := len(audit.byAction(guardrailBaselineSetAction)); n != wantEntries {
				t.Fatalf("audit entries = %d, want %d", n, wantEntries)
			}
			if changed != wantChanged {
				t.Fatalf("onChange ran %d times, want %d (the gateway must re-read on a switch)", changed, wantChanged)
			}
			// Setting the mode never configures a policy: the baseline stays on.
			if st.GetResponsePolicy() != pb.GuardrailEffectivePolicy_GUARDRAIL_EFFECTIVE_POLICY_BASELINE {
				t.Fatalf("response policy after a %s set = %v, want BASELINE", tc.name, st.GetResponsePolicy())
			}
		})
	}
}

// TestSetBaselineMode_AuditEntryNamesCaller (#2738): every switch writes one
// guardrail.baseline.set entry attributed to the caller, with the previous
// and new mode and revision.
func TestSetBaselineMode_AuditEntryNamesCaller(t *testing.T) {
	s, audit := newGPServer()
	first, err := gpSetMode(gpAdminCtx(), s, gpModeBlock)
	if err != nil {
		t.Fatalf("set BLOCK: %v", err)
	}
	if first.GetBaseline().GetMode() != gpModeBlock || first.GetBaseline().GetRevision() != 1 || first.GetBaseline().GetUpdatedBy() != "ops" {
		t.Fatalf("response = %v, want BLOCK at revision 1 by ops", first.GetBaseline())
	}
	if _, err := gpSetMode(gpAdminCtx(), s, gpModeLog); err != nil {
		t.Fatalf("set LOG: %v", err)
	}
	entries := audit.byAction(guardrailBaselineSetAction)
	if len(entries) != 2 {
		t.Fatalf("audit entries = %d, want 2", len(entries))
	}
	want := []guardrailBaselineAuditDetail{
		{PreviousMode: "GUARDRAIL_BASELINE_MODE_LOG", PreviousRevision: 0, Mode: "GUARDRAIL_BASELINE_MODE_BLOCK", Revision: 1},
		{PreviousMode: "GUARDRAIL_BASELINE_MODE_BLOCK", PreviousRevision: 1, Mode: "GUARDRAIL_BASELINE_MODE_LOG", Revision: 2},
	}
