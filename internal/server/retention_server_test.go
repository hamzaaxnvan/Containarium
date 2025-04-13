package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/retention"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func newRetentionTestServer() (*RetentionServer, *fakeAuditLogger) {
	s := NewRetentionServer(retention.NewMemoryStore())
	a := &fakeAuditLogger{}
	s.SetAuditStore(a)
	return s, a
}

func promptsDays(days int32) *pb.SetRetentionPolicyRequest {
	return &pb.SetRetentionPolicyRequest{Windows: []*pb.RetentionWindow{
		{Class: pb.RetentionClass_RETENTION_CLASS_PROMPTS, Days: days},
	}}
}

// TestRetentionServer_AdminOnly: every RPC refuses a non-admin and an
// unauthenticated caller, and a refused change writes no audit entry.
func TestRetentionServer_AdminOnly(t *testing.T) {
	calls := map[string]func(context.Context, *RetentionServer) error{
		"get policy": func(ctx context.Context, s *RetentionServer) error {
			_, err := s.GetRetentionPolicy(ctx, &pb.GetRetentionPolicyRequest{})
			return err
		},
		"get report": func(ctx context.Context, s *RetentionServer) error {
			_, err := s.GetRetentionReport(ctx, &pb.GetRetentionReportRequest{})
			return err
		},
		"get mode": func(ctx context.Context, s *RetentionServer) error {
			_, err := s.GetRetentionMode(ctx, &pb.GetRetentionModeRequest{})
			return err
		},
		"set mode": func(ctx context.Context, s *RetentionServer) error {
			_, err := s.SetRetentionMode(ctx, &pb.SetRetentionModeRequest{Mode: pb.RetentionMode_RETENTION_MODE_ENFORCE})
			return err
		},
		"set policy": func(ctx context.Context, s *RetentionServer) error {
			_, err := s.SetRetentionPolicy(ctx, promptsDays(7))
			return err
		},
		"list holds": func(ctx context.Context, s *RetentionServer) error {
			_, err := s.ListLegalHolds(ctx, &pb.ListLegalHoldsRequest{})
			return err
		},
		"set hold": func(ctx context.Context, s *RetentionServer) error {
			_, err := s.SetLegalHold(ctx, &pb.SetLegalHoldRequest{OrgId: "org-a", Reason: "case"})
			return err
		},
		"release hold": func(ctx context.Context, s *RetentionServer) error {
			_, err := s.ReleaseLegalHold(ctx, &pb.ReleaseLegalHoldRequest{OrgId: "org-a"})
			return err
		},
	}
	callers := map[string]struct {
		ctx  context.Context
		want codes.Code
	}{
		"non-admin":       {gpUserCtx(), codes.PermissionDenied},
		"unauthenticated": {context.Background(), codes.Unauthenticated},
	}
	for name, call := range calls {
		for who, c := range callers {
			t.Run(name+" as "+who, func(t *testing.T) {
				s, a := newRetentionTestServer()
				if _, err := s.SetLegalHold(gpAdminCtx(), &pb.SetLegalHoldRequest{OrgId: "org-a", Reason: "seed"}); err != nil {
					t.Fatalf("seed hold: %v", err)
				}
				before := len(a.entries)
				if got := status.Code(call(c.ctx, s)); got != c.want {
					t.Fatalf("%s as %s = %v, want %v", name, who, got, c.want)
				}
				if len(a.entries) != before {
					t.Fatalf("a refused call wrote %d audit entries", len(a.entries)-before)
				}
			})
		}
	}
}

// TestRetentionServer_ChangesAreAuditedWithTheCaller: a set, a hold and a
// release each write one entry naming the admin who made it. The hold is
// released by a different admin, so the release entry must name the releaser
// while its detail keeps who set the hold.
func TestRetentionServer_ChangesAreAuditedWithTheCaller(t *testing.T) {
	s, a := newRetentionTestServer()
	ctx := gpAdminCtx()
	resp, err := s.SetRetentionPolicy(ctx, promptsDays(7))
	if err != nil {
		t.Fatalf("SetRetentionPolicy: %v", err)
	}
	if resp.GetPolicy().GetRevision() != 1 || resp.GetPolicy().GetUpdatedBy() != "ops" {
		t.Fatalf("policy = %v, want revision 1 by ops", resp.GetPolicy())
	}
	if _, err := s.SetLegalHold(ctx, &pb.SetLegalHoldRequest{OrgId: "org-a", Reason: "case 7"}); err != nil {
		t.Fatalf("SetLegalHold: %v", err)
	}
	releaser := auth.ContextWithTestSubject(context.Background(), "ops-2", auth.RoleAdmin)
	if _, err := s.ReleaseLegalHold(releaser, &pb.ReleaseLegalHoldRequest{OrgId: "org-a"}); err != nil {
		t.Fatalf("ReleaseLegalHold: %v", err)
	}
	entry := func(action, by string) string {
		t.Helper()
		got := a.byAction(action)
		if len(got) != 1 || got[0].Username != by {
			t.Fatalf("%s entries = %+v, want one by %s", action, got, by)
		}
		return got[0].Detail
	}
	var pol retentionPolicyAuditDetail
	if err := json.Unmarshal([]byte(entry(retentionPolicySetAction, "ops")), &pol); err != nil {
		t.Fatalf("policy detail: %v", err)
	}
	wantPol := retentionPolicyAuditDetail{PreviousRevision: 0, Revision: 1,
		Windows: []retentionAuditWindow{{Class: "RETENTION_CLASS_PROMPTS", Days: 7}}}
	if fmt.Sprint(pol) != fmt.Sprint(wantPol) {
		t.Errorf("policy detail = %+v, want %+v", pol, wantPol)
	}
	for action, by := range map[string]string{retentionHoldSetAction: "ops", retentionHoldReleaseAction: "ops-2"} {
		var hold retentionHoldAuditDetail
		if err := json.Unmarshal([]byte(entry(action, by)), &hold); err != nil {
			t.Fatalf("%s detail: %v", action, err)
		}
		if hold.OrgID != "org-a" || hold.Reason != "case 7" || hold.SetBy != "ops" {
			t.Errorf("%s detail = %+v, want org-a, case 7, set by ops", action, hold)
		}
	}
}

// TestRetentionServer_FailedAuditFailsTheChange: when the audit entry cannot
// be written, the call fails with INTERNAL and nothing changes.
func TestRetentionServer_FailedAuditFailsTheChange(t *testing.T) {
	ctx := gpAdminCtx()
	s, a := newRetentionTestServer()
	if _, err := s.SetLegalHold(ctx, &pb.SetLegalHoldRequest{OrgId: "org-held", Reason: "seed"}); err != nil {
		t.Fatalf("seed hold: %v", err)
	}
	a.err = errors.New("audit store down")

	if _, err := s.SetRetentionPolicy(ctx, promptsDays(7)); status.Code(err) != codes.Internal {
		t.Fatalf("SetRetentionPolicy with audit down = %v, want Internal", err)
	}
	if _, err := s.SetLegalHold(ctx, &pb.SetLegalHoldRequest{OrgId: "org-new", Reason: "case"}); status.Code(err) != codes.Internal {
		t.Fatalf("SetLegalHold with audit down = %v, want Internal", err)
	}
	if _, err := s.ReleaseLegalHold(ctx, &pb.ReleaseLegalHoldRequest{OrgId: "org-held"}); status.Code(err) != codes.Internal {
		t.Fatalf("ReleaseLegalHold with audit down = %v, want Internal", err)
	}
	p, _ := s.GetRetentionPolicy(ctx, &pb.GetRetentionPolicyRequest{})
