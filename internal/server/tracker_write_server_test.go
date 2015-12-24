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
