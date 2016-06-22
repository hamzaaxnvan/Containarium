package tracker

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClaimProvider is a WriterProvider double simulating one issue's
// mutable state (an ordered comment list, an assignee) — enough for
// ClaimTrackerIssue's tests without a real HTTP call. Every method
// this test exercises is implemented for real; the rest are stubs
// satisfying the interface.
type fakeClaimProvider struct {
	mu         sync.Mutex
	comments   []Comment
	assignee   string
	clock      *fakeClock
	onComment  func() // called once, at the start of the first Comment call
	commentIdx int
}

func (f *fakeClaimProvider) GetIssue(ctx context.Context, c Conn, number int64) (Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Comment, len(f.comments))
	copy(out, f.comments)
	return Issue{Number: number, Assignee: f.assignee, Comments: out}, nil
}

func (f *fakeClaimProvider) Comment(ctx context.Context, c Conn, number int64, body string) (Comment, error) {
	f.mu.Lock()
	hook := f.onComment
	if f.commentIdx == 0 {
		f.onComment = nil
	}
	f.commentIdx++
	f.mu.Unlock()

	if hook != nil {
		hook()
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	comment := Comment{Author: "bot", CreatedAt: f.clock.Now(), Body: body}
	f.comments = append(f.comments, comment)
	return comment, nil
}

func (f *fakeClaimProvider) AssignIfUnassigned(ctx context.Context, c Conn, number int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.assignee != "" {
		return false, nil
	}
	f.assignee = "bot"
	return true, nil
}

func (f *fakeClaimProvider) appendRaw(c Comment) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments = append(f.comments, c)
}

func (f *fakeClaimProvider) ListIssues(context.Context, Conn, IssueFilter) ([]Issue, error) {
	return nil, nil
}
func (f *fakeClaimProvider) GetChange(context.Context, Conn, int64) (Change, error) {
	return Change{}, nil
}
func (f *fakeClaimProvider) SetLabels(context.Context, Conn, int64, []string, []string) error {
	return nil
}
func (f *fakeClaimProvider) DescribeCredential(context.Context, Conn) (CredentialInfo, error) {
	return CredentialInfo{}, nil
}
func (f *fakeClaimProvider) WhoAmI(context.Context, Conn) (string, error) {
	return "bot", nil // matches Comment's own hardcoded Author, below
}
func (f *fakeClaimProvider) CreateIssue(context.Context, Conn, NewIssue) (Issue, error) {
	return Issue{}, nil
}

var _ WriterProvider = (*fakeClaimProvider)(nil)

type fakeResolver map[string]bool

func (f fakeResolver) Live(runID string) bool { return f[runID] }

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func TestClaim_NoExistingClaim_ClaimsAndAssigns(t *testing.T) {
	clock := newFakeClock()
	provider := &fakeClaimProvider{clock: clock}
	id := Identity{RunID: "run-1", SkillID: "reviewer"}

	result, err := ClaimTrackerIssue(context.Background(), provider, Conn{}, 1, id, fakeResolver{}, clock, time.Hour)
	if err != nil {
		t.Fatalf("ClaimTrackerIssue: %v", err)
	}
	if !result.Claimed {
		t.Error("Claimed = false, want true")
	}
	if !result.Assigned {
		t.Error("Assigned = false, want true (issue had no assignee)")
	}
	if len(provider.comments) != 1 {
		t.Fatalf("posted %d comments, want exactly 1", len(provider.comments))
	}
	runID, _, kind, ok := ParseMarker(provider.comments[0].Body)
	if !ok || runID != "run-1" || kind != KindClaim {
		t.Errorf("posted comment marker = (%q, %q, ok=%v), want (run-1, claim, true)", runID, kind, ok)
	}
}

func TestClaim_RefusesLiveForeignClaim(t *testing.T) {
	clock := newFakeClock()
	provider := &fakeClaimProvider{clock: clock}
	provider.appendRaw(Comment{Author: "bot", CreatedAt: clock.Now(), Body: Stamp(Identity{RunID: "other-run", SkillID: "s"}, KindClaim)})

	result, err := ClaimTrackerIssue(context.Background(), provider, Conn{}, 1, Identity{RunID: "my-run", SkillID: "s"},
		fakeResolver{"other-run": true}, clock, time.Hour)
	if err != nil {
		t.Fatalf("ClaimTrackerIssue: %v", err)
	}
	if result.Claimed {
		t.Error("Claimed = true, want false — a live foreign claim exists")
	}
	if result.AlreadyClaimedByRunID != "other-run" {
		t.Errorf("AlreadyClaimedByRunID = %q, want other-run", result.AlreadyClaimedByRunID)
	}
	if len(provider.comments) != 1 {
		t.Errorf("posted %d comments, want 0 new ones (only the pre-seeded claim)", len(provider.comments)-1)
	}
}

func TestClaim_RefusesFreshUnknownRun(t *testing.T) {
	clock := newFakeClock()
	provider := &fakeClaimProvider{clock: clock}
	provider.appendRaw(Comment{Author: "bot", CreatedAt: clock.Now(), Body: Stamp(Identity{RunID: "other-run", SkillID: "s"}, KindClaim)})
	clock.Advance(30 * time.Minute) // younger than the 2h default staleAfter

	result, err := ClaimTrackerIssue(context.Background(), provider, Conn{}, 1, Identity{RunID: "my-run", SkillID: "s"},
		fakeResolver{}, clock, DefaultStaleAfter) // resolver doesn't know "other-run" (not live)
	if err != nil {
		t.Fatalf("ClaimTrackerIssue: %v", err)
	}
	if result.Claimed {
		t.Error("Claimed = true, want false — the foreign claim is still fresh even though not Live")
	}
	if result.AlreadyClaimedByRunID != "other-run" {
		t.Errorf("AlreadyClaimedByRunID = %q, want other-run", result.AlreadyClaimedByRunID)
	}
}

func TestClaim_TakesOverStaleClaim(t *testing.T) {
	clock := newFakeClock()
	provider := &fakeClaimProvider{clock: clock}
	provider.appendRaw(Comment{Author: "bot", CreatedAt: clock.Now(), Body: Stamp(Identity{RunID: "other-run", SkillID: "s"}, KindClaim)})
	clock.Advance(3 * time.Hour) // older than the 2h default staleAfter

	result, err := ClaimTrackerIssue(context.Background(), provider, Conn{}, 1, Identity{RunID: "my-run", SkillID: "s"},
		fakeResolver{}, clock, DefaultStaleAfter)
	if err != nil {
		t.Fatalf("ClaimTrackerIssue: %v", err)
	}
	if !result.Claimed {
		t.Errorf("Claimed = false, want true — the foreign claim is stale and not live")
	}
	if len(provider.comments) != 2 {
		t.Fatalf("posted comments = %d, want 2 (the original stale one plus our new claim)", len(provider.comments))
	}
	runID, _, kind, _ := ParseMarker(provider.comments[1].Body)
	if runID != "my-run" || kind != KindClaim {
		t.Errorf("second comment marker = (%q, %q), want (my-run, claim)", runID, kind)
	}
}

func TestClaim_IdempotentForSameRun(t *testing.T) {
	clock := newFakeClock()
	provider := &fakeClaimProvider{clock: clock}
	provider.appendRaw(Comment{Author: "bot", CreatedAt: clock.Now(), Body: Stamp(Identity{RunID: "my-run", SkillID: "s"}, KindClaim)})

	result, err := ClaimTrackerIssue(context.Background(), provider, Conn{}, 1, Identity{RunID: "my-run", SkillID: "s"},
