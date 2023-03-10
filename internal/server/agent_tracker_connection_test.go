package server

import (
	"context"
	"testing"

	"github.com/footprintai/containarium/internal/tracker"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeTrackerConnectionChecker is a trackerConnectionChecker double,
// recording the (username, name) it was asked about so tests can pin
// that RunAgentSkill validates against the CALLER's own tenant — never
// the request's own claimed identity or the box's synthetic tenant name.
type fakeTrackerConnectionChecker struct {
	gotUsername, gotName string
	conn                 *tracker.Connection
	err                  error
}

func (f *fakeTrackerConnectionChecker) Get(_ context.Context, username, name string) (*tracker.Connection, error) {
	f.gotUsername, f.gotName = username, name
	return f.conn, f.err
}

func TestValidateTrackerConnection_Empty_AlwaysPasses(t *testing.T) {
	s := &AgentSkillServer{} // no trackerConnections configured at all
	if err := s.validateTrackerConnection(context.Background(), ""); err != nil {
		t.Errorf("empty tracker_connection must always pass, even with no store: %v", err)
	}
}

func TestValidateTrackerConnection_NoStoreConfigured_FailsClosed(t *testing.T) {
	s := &AgentSkillServer{}
	err := s.validateTrackerConnection(ctxAs("alice", false), "default")
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition (no tracker store wired)", status.Code(err))
	}
}

func TestValidateTrackerConnection_NoAuthContext_Unauthenticated(t *testing.T) {
	s := &AgentSkillServer{trackerConnections: &fakeTrackerConnectionChecker{}}
	err := s.validateTrackerConnection(context.Background(), "default")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestValidateTrackerConnection_NotFoundForTenant_InvalidArgument(t *testing.T) {
	checker := &fakeTrackerConnectionChecker{err: tracker.ErrNotFound}
	s := &AgentSkillServer{trackerConnections: checker}
	err := s.validateTrackerConnection(ctxAs("alice", false), "default")
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument (no such connection for this tenant)", status.Code(err))
	}
}

func TestValidateTrackerConnection_ValidatesAgainstCallerTenant(t *testing.T) {
	checker := &fakeTrackerConnectionChecker{conn: &tracker.Connection{Username: "alice", Name: "default"}}
	s := &AgentSkillServer{trackerConnections: checker}
	if err := s.validateTrackerConnection(ctxAs("alice", false), "default"); err != nil {
		t.Fatalf("validateTrackerConnection: %v", err)
	}
	if checker.gotUsername != "alice" {
		t.Errorf("checked tenant = %q, want the CALLER's own username (alice), not a request field or box name", checker.gotUsername)
	}
	if checker.gotName != "default" {
		t.Errorf("checked connection name = %q, want default", checker.gotName)
	}
}
