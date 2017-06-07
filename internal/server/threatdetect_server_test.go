package server

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/netbpf"
	"github.com/footprintai/containarium/internal/threatdetect"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestGetSentryStatus_Disabled(t *testing.T) {
	s := NewThreatDetectionServer(nil, false, false, "")
	resp, err := s.GetSentryStatus(secReadCtx(), &pb.GetSentryStatusRequest{})
	if err != nil {
		t.Fatalf("GetSentryStatus: %v", err)
	}
	if resp.State != pb.SentryState_SENTRY_STATE_DISABLED {
		t.Errorf("state = %v, want DISABLED", resp.State)
	}
	if resp.Reason == "" {
		t.Error("reason is empty, want an explanation")
	}
}

// Enabled but no eBPF object loaded: must report UNAVAILABLE with a reason,
// never silently "no findings" — the #1640 acceptance criterion.
func TestGetSentryStatus_UnavailableWithoutEBPF(t *testing.T) {
	s := NewThreatDetectionServer(nil, true, false, "eBPF object not loaded (set CONTAINARIUM_NETWORK_POLICY_BPF_OBJECT)")
	resp, err := s.GetSentryStatus(secReadCtx(), &pb.GetSentryStatusRequest{})
	if err != nil {
		t.Fatalf("GetSentryStatus: %v", err)
	}
	if resp.State != pb.SentryState_SENTRY_STATE_UNAVAILABLE {
		t.Errorf("state = %v, want UNAVAILABLE", resp.State)
	}
	if resp.Reason != "eBPF object not loaded (set CONTAINARIUM_NETWORK_POLICY_BPF_OBJECT)" {
		t.Errorf("reason = %q, want the wired unavailableReason", resp.Reason)
	}
}

func TestGetSentryStatus_Degraded(t *testing.T) {
	engine := threatdetect.NewEngine(&fakeFindingSink{}, "backend-1", true /* degraded */, nil, nil)
	s := NewThreatDetectionServer(engine, true, true, "")
	resp, err := s.GetSentryStatus(secReadCtx(), &pb.GetSentryStatusRequest{})
	if err != nil {
		t.Fatalf("GetSentryStatus: %v", err)
	}
	if resp.State != pb.SentryState_SENTRY_STATE_DEGRADED {
		t.Errorf("state = %v, want DEGRADED", resp.State)
	}
	if resp.Reason != "DEGRADED (no persistence): responses disabled" {
		t.Errorf("reason = %q, want the process-responder degraded line (#2636)", resp.Reason)
	}
}

func TestGetSentryStatus_OKWithRuleHealth(t *testing.T) {
	engine := threatdetect.NewEngine(&fakeFindingSink{}, "backend-1", false, nil, nil)
	engine.Register(&fakeRule{id: pb.ThreatRuleId_THREAT_RULE_ID_BAD_DESTINATION})
	s := NewThreatDetectionServer(engine, true, true, "")

	resp, err := s.GetSentryStatus(secReadCtx(), &pb.GetSentryStatusRequest{})
	if err != nil {
		t.Fatalf("GetSentryStatus: %v", err)
	}
	if resp.State != pb.SentryState_SENTRY_STATE_OK {
		t.Errorf("state = %v, want OK", resp.State)
	}
	if len(resp.Rules) != 1 || resp.Rules[0].Rule != pb.ThreatRuleId_THREAT_RULE_ID_BAD_DESTINATION || !resp.Rules[0].Healthy {
		t.Errorf("rules = %+v, want one healthy BAD_DESTINATION entry", resp.Rules)
	}
}

func TestListBadDestinations_NoRule_ReturnsUnavailable(t *testing.T) {
	s := NewThreatDetectionServer(nil, false, false, "")
	if _, err := s.ListBadDestinations(secReadCtx(), &pb.ListBadDestinationsRequest{}); err == nil {
		t.Fatal("ListBadDestinations with no rule wired should error, got nil")
	}
}

// The bad-destination blocklist is fleet-wide, so mutating it now requires the
// admin role AND security:write (#1717). These tests exercise the round-trip
// behaviour, not the gate — the gate has its own tests in
// authguard_writepath_test.go.
// GetSentryStatus and ListBadDestinations gained security:read in #1718.
func secReadCtx() context.Context {
	return auth.ContextWithTestSubjectScopes(context.Background(), "ops",
		[]string{"user"}, []string{auth.ScopeSecurityRead})
}

func badDestAdminCtx() context.Context {
	return auth.ContextWithTestSubjectScopes(context.Background(), "ops",
		[]string{auth.RoleAdmin}, []string{auth.ScopeSecurityWrite})
}

func TestAddThenListBadDestination(t *testing.T) {
	s := NewThreatDetectionServer(nil, false, false, "")
