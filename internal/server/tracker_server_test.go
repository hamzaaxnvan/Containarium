package server

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/tracker"
	"github.com/footprintai/containarium/internal/tracker/submit"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeReaderProvider is a tracker.ReaderProvider double for testing
// GetTrackerStatus, SetTrackerConnection's best-effort expiry
// population, and the GetTrackerIssue/ListTrackerIssues/GetTrackerChange
// handlers, without a real GitHub/GitLab call — those are covered by
// internal/tracker/github and internal/tracker/gitlab's own httptest-based
// suites.
type fakeReaderProvider struct {
	credInfo tracker.CredentialInfo
	credErr  error

	issue    tracker.Issue
	issueErr error

	issues    []tracker.Issue
	issuesErr error

	change    tracker.Change
	changeErr error
}

func (f *fakeReaderProvider) DescribeCredential(context.Context, tracker.Conn) (tracker.CredentialInfo, error) {
	return f.credInfo, f.credErr
}

func (f *fakeReaderProvider) GetIssue(context.Context, tracker.Conn, int64) (tracker.Issue, error) {
	return f.issue, f.issueErr
}

func (f *fakeReaderProvider) ListIssues(context.Context, tracker.Conn, tracker.IssueFilter) ([]tracker.Issue, error) {
	return f.issues, f.issuesErr
}

func (f *fakeReaderProvider) GetChange(context.Context, tracker.Conn, int64) (tracker.Change, error) {
	return f.change, f.changeErr
}

func fakeDescriberSet(d tracker.ReaderProvider) map[pb.TrackerProvider]tracker.ReaderProvider {
	return map[pb.TrackerProvider]tracker.ReaderProvider{
		pb.TrackerProvider_TRACKER_PROVIDER_GITHUB: d,
		pb.TrackerProvider_TRACKER_PROVIDER_GITLAB: d,
	}
}

// mustTestTrackerStore returns a real tracker.Store against
// CONTAINARIUM_TEST_DSN, skipping the test if it isn't set — same
// convention as mustTestSecretsStore in secrets_kms_key_test.go.
func mustTestTrackerStore(t *testing.T) *tracker.Store {
	t.Helper()
	dsn := os.Getenv("CONTAINARIUM_TEST_DSN")
	if dsn == "" {
		t.Skip("set CONTAINARIUM_TEST_DSN to run this against Postgres")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect Postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	store, err := tracker.NewStore(ctx, pool)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func TestSetTrackerConnection_NoAuthContext(t *testing.T) {
	s := &ContainerServer{}
	_, err := s.SetTrackerConnection(context.Background(), &pb.SetTrackerConnectionRequest{
		Username: "alice", Name: "default",
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", status.Code(err))
	}
}

// TestSetTrackerConnection_TrackerWriteScopeInsufficient pins the
// deliberate separation from #1922's agent-facing scopes: a token
// carrying tracker:write (what an agent's run-scoped JWT gets for the
// issue/comment/claim verbs) must NOT be able to create or repoint a
// connection — that needs tracker:admin.
func TestSetTrackerConnection_TrackerWriteScopeInsufficient(t *testing.T) {
	s := &ContainerServer{}
	ctx := kmsKeyTestCtx("alice", "member", "tracker:write")
	_, err := s.SetTrackerConnection(ctx, &pb.SetTrackerConnectionRequest{
		Username: "alice", Name: "default",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", status.Code(err))
	}
}

// TestSetTrackerConnection_CrossTenantDenied needs a configured store —
// with a nil store, the handler's nil-store check (which runs before
// AuthorizeTenant, same ordering as SetSecret/GetSecret/etc.) would
// return Unavailable before authz is ever reached, testing the wrong
// thing.
func TestSetTrackerConnection_CrossTenantDenied(t *testing.T) {
	s := &ContainerServer{secretsStore: mustTestSecretsStore(t), trackerStore: mustTestTrackerStore(t)}
	ctx := kmsKeyTestCtx("tracker-rpc-cross-tenant-alice", "member", "tracker:admin")
	_, err := s.SetTrackerConnection(ctx, &pb.SetTrackerConnectionRequest{
