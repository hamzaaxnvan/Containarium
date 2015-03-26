package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/tracker"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mustTestAuditStore returns a real audit.Store against
// CONTAINARIUM_TEST_DSN, skipping the test if it isn't set — same
// convention as mustTestTrackerStore.
func mustTestAuditStore(t *testing.T) *audit.Store {
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
	store, err := audit.NewStore(ctx, pool)
	if err != nil {
		t.Fatalf("audit.NewStore: %v", err)
	}
	return store
}

// fakeWriterProvider extends fakeReaderProvider with the write verbs, for
// testing the tracker write-verb RPCs (CommentOnTrackerIssue here;
// ClaimTrackerIssue and SetTrackerIssueLabels in their own follow-up PRs)
// without a real GitHub/GitLab call — those adapters' own request/response
// shapes are covered by internal/tracker/github and internal/tracker/gitlab.
type fakeWriterProvider struct {
	fakeReaderProvider

	commentBody string
	commentOut  tracker.Comment
	commentErr  error

	assignCalled bool
	assignOut    bool
	assignErr    error

	labelsAdd, labelsRemove []string
	labelsErr               error

	// #2024: CreateIssue records every request; commentNumbers/
	// commentBodies record every Comment call (commentBody above keeps
	// only the last, which the create tests' back-link assertions can't
	// use — they need "exactly one comment, on the parent").
	createIssueReqs []tracker.NewIssue
	createIssueErr  error
	// afterCreateIssue runs once the "forge" has accepted a create — the
	// window the review of #2034 found (caller cancels after upstream success).
	afterCreateIssue func()
	commentNumbers   []int64
	commentBodies    []string

	openChangeReq tracker.OpenChangeRequest
	openChangeOut tracker.Change
	openChangeErr error

	defaultBranchOut string
	defaultBranchErr error

	// #3288: FindOpenChange's canned answer; the zero value is "no open
	// change", so existing callers still reach OpenChange.
	findOpenBranch string
	findOpenOut    tracker.Change
	findOpenFound  bool
	findOpenErr    error
}

// FindOpenChange records the branch asked about and returns the canned
// answer.
func (f *fakeWriterProvider) FindOpenChange(_ context.Context, _ tracker.Conn, headBranch string) (tracker.Change, bool, error) {
	f.findOpenBranch = headBranch
	return f.findOpenOut, f.findOpenFound, f.findOpenErr
}

func (f *fakeWriterProvider) Comment(_ context.Context, _ tracker.Conn, number int64, body string) (tracker.Comment, error) {
	f.commentBody = body
	f.commentNumbers = append(f.commentNumbers, number)
	f.commentBodies = append(f.commentBodies, body)
	if f.commentErr != nil {
		return tracker.Comment{}, f.commentErr
	}
	out := f.commentOut
	out.Body = body
	return out, nil
}

func (f *fakeWriterProvider) AssignIfUnassigned(context.Context, tracker.Conn, int64) (bool, error) {
	f.assignCalled = true
	return f.assignOut, f.assignErr
}

func (f *fakeWriterProvider) SetLabels(_ context.Context, _ tracker.Conn, _ int64, add, remove []string) error {
	f.labelsAdd, f.labelsRemove = add, remove
	return f.labelsErr
}

// CreateIssue records the request and echoes it back as a freshly
// numbered open issue (501, 502, ...) so a test can tell children apart.
func (f *fakeWriterProvider) CreateIssue(ctx context.Context, _ tracker.Conn, n tracker.NewIssue) (tracker.Issue, error) {
	if f.createIssueErr != nil {
		return tracker.Issue{}, f.createIssueErr
	}
	f.createIssueReqs = append(f.createIssueReqs, n) // the forge accepted the POST
	if f.afterCreateIssue != nil {
		f.afterCreateIssue()
	}
	// Like a real HTTP adapter: if the context was cancelled while the
	// request was in flight, the response is never read and the call
	// fails with ctx.Err() — even though the issue now exists upstream.
	if err := ctx.Err(); err != nil {
		return tracker.Issue{}, err
	}
	return tracker.Issue{
		Number: int64(500 + len(f.createIssueReqs)),
		Title:  n.Title,
		Body:   n.Body,
		State:  pb.TrackerIssueState_TRACKER_ISSUE_STATE_OPEN,
		Labels: n.Labels,
	}, nil
}

// WhoAmI returns the zero value, matching this fake's own Comment
// output (commentOut.Author defaults to "") and every fixture comment
// in this file (none set Author) — so ClaimTrackerIssue's marker
// authentication (#1922, caught in review of #1924) treats them all as
// trusted without needing every existing fixture updated.
func (f *fakeWriterProvider) WhoAmI(context.Context, tracker.Conn) (string, error) {
	return "", nil
}

// OpenChange records the request and returns a canned result — added
// (#1923) so this fake satisfies the full tracker.Provider interface,
// which SubmitTrackerChange's connection resolution requires; every
// existing caller that never sets openChangeOut/openChangeErr gets the
// zero Change and no error, same "safe default" shape as the other
// fields here.
func (f *fakeWriterProvider) OpenChange(_ context.Context, _ tracker.Conn, req tracker.OpenChangeRequest) (tracker.Change, error) {
	f.openChangeReq = req
	return f.openChangeOut, f.openChangeErr
}

// DefaultBranch returns a canned value (default "main"), matching this
// fake's own "safe default, override when a test needs to prove
// something about it" convention.
func (f *fakeWriterProvider) DefaultBranch(context.Context, tracker.Conn) (string, error) {
	if f.defaultBranchErr != nil {
		return "", f.defaultBranchErr
	}
	if f.defaultBranchOut == "" {
		return "main", nil
	}
	return f.defaultBranchOut, nil
}

// setUpWriterConnection is setUpBrokerConnection's write-verb
// counterpart: a broker-only secret and a connection referencing it,
// with a wired ClaimLocks and a run-scoped write context ready to use.
func setUpWriterConnection(t *testing.T, user string, provider *fakeWriterProvider) (*ContainerServer, context.Context) {
	t.Helper()
	s := &ContainerServer{
		secretsStore:      mustTestSecretsStore(t),
		trackerStore:      mustTestTrackerStore(t),
		trackerDescribers: fakeDescriberSet(provider),
		claimLocks:        tracker.NewClaimLocks(),
	}
	secretCtx := kmsKeyTestCtx(user, "member", "secrets:write")
	if _, err := s.SetSecret(secretCtx, &pb.SetSecretRequest{
		Username: user, Name: "GH_TOKEN", Value: "ghp_x",
		DeliveryMode: pb.SecretDelivery_SECRET_DELIVERY_BROKER_ONLY,
	}); err != nil {
		t.Fatalf("SetSecret (broker-only): %v", err)
	}
	adminCtx := kmsKeyTestCtx(user, "member", "tracker:admin")
	if _, err := s.SetTrackerConnection(adminCtx, &pb.SetTrackerConnectionRequest{
		Username: user, Name: "default",
		Provider: pb.TrackerProvider_TRACKER_PROVIDER_GITHUB,
		Project:  "acme/widgets", CredentialSecret: "GH_TOKEN",
	}); err != nil {
		t.Fatalf("SetTrackerConnection: %v", err)
	}
	writeCtx := auth.ContextWithTestRunID(kmsKeyTestCtx(user, "member", "tracker:write"), "run-abc123")
	return s, writeCtx
}

// ---- CommentOnTrackerIssue ----------------------------------------------

func TestCommentOnTrackerIssue_NoAuthContext(t *testing.T) {
	s := &ContainerServer{}
	_, err := s.CommentOnTrackerIssue(context.Background(), &pb.CommentOnTrackerIssueRequest{
		Username: "alice", Connection: "default", Number: 1, Body: "hi",
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestCommentOnTrackerIssue_TrackerReadScopeInsufficient(t *testing.T) {
	s := &ContainerServer{}
	ctx := kmsKeyTestCtx("alice", "member", "tracker:read")
	_, err := s.CommentOnTrackerIssue(ctx, &pb.CommentOnTrackerIssueRequest{
		Username: "alice", Connection: "default", Number: 1, Body: "hi",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (tracker:read must not grant a write verb)", status.Code(err))
	}
}

func TestCommentOnTrackerIssue_RejectsEmptyBody(t *testing.T) {
	s := &ContainerServer{trackerStore: mustTestTrackerStore(t)}
	ctx := kmsKeyTestCtx("alice", "member", "tracker:write")
	_, err := s.CommentOnTrackerIssue(ctx, &pb.CommentOnTrackerIssueRequest{
		Username: "alice", Connection: "default", Number: 1,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument (empty body)", status.Code(err))
	}
}

// TestCommentOnTrackerIssue_StampsSanitizesAndAudits is the happy path:
// a run-scoped caller's comment reaches the provider sanitized and
// stamped with its run identity, and an audit row is written.
func TestCommentOnTrackerIssue_StampsSanitizesAndAudits(t *testing.T) {
	// Unique per invocation: fixed usernames accumulate audit rows when
