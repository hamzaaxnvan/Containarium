package server

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/runlease"
	"github.com/footprintai/containarium/internal/tracker"
	"github.com/footprintai/containarium/internal/tracker/submit"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeSubmitBox is a submit.BoxRunner test double standing in for a
// real box: ExecWithOutput answers ExtractBundle's two scripted calls
// (bundle create + rev-parse, then stat/wc) without running anything,
// and ReadFile returns pre-built bundle bytes.
type fakeSubmitBox struct {
	bundleBytes []byte
	headSHA     string
	execErr     error
}

func (f *fakeSubmitBox) ExecWithOutput(_ string, cmd []string) (string, string, error) {
	if f.execErr != nil {
		return "", "boom", f.execErr
	}
	script := strings.Join(cmd, " ")
	if strings.Contains(script, "stat -c") || strings.Contains(script, "wc -c") {
		return strconv.Itoa(len(f.bundleBytes)), "", nil
	}
	return f.headSHA + "\n", "", nil
}

func (f *fakeSubmitBox) ReadFile(_ string, _ string) ([]byte, error) {
	return f.bundleBytes, nil
}

// fakeSubmitPusher is a submit.GitPusher test double recording the
// PushSpec it was called with and returning a canned result.
type fakeSubmitPusher struct {
	gotSpec submit.PushSpec
	result  submit.PushResult
	err     error
}

func (f *fakeSubmitPusher) PushBundle(_ context.Context, spec submit.PushSpec) (submit.PushResult, error) {
	f.gotSpec = spec
	return f.result, f.err
}

// realBundleBytes builds an actual git bundle (base commit + one commit
// on top, bundled as base..HEAD) so tests exercising the happy path
// pass through ExtractBundle's real `git bundle list-heads` validation
// rather than a hand-rolled fixture that might not match git's format.
func realBundleBytes(t *testing.T) (bundle []byte, headSHA string) {
	t.Helper()
	dir := t.TempDir()
	runGitFixture(t, dir, "init", "-q", "-b", "main")
	runGitFixture(t, dir, "config", "user.email", "t@example.com")
	runGitFixture(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, dir, "add", "f.txt")
	runGitFixture(t, dir, "commit", "-q", "-m", "base")
	base := strings.TrimSpace(runGitFixtureOutput(t, dir, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("head"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, dir, "add", "f.txt")
	runGitFixture(t, dir, "commit", "-q", "-m", "head")
	headSHA = strings.TrimSpace(runGitFixtureOutput(t, dir, "rev-parse", "HEAD"))
	bundlePath := filepath.Join(dir, "out.bundle")
	runGitFixture(t, dir, "bundle", "create", bundlePath, base+"..HEAD")
	b, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	return b, headSHA
}

func runGitFixture(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // test-only, fixed argv
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func runGitFixtureOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // test-only, fixed argv
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}

func TestSubmitTrackerChange_NoAuthContext(t *testing.T) {
	s := &ContainerServer{}
	_, err := s.SubmitTrackerChange(context.Background(), &pb.SubmitTrackerChangeRequest{
		Username: "alice", Connection: "default", Issue: 1, Title: "t",
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestSubmitTrackerChange_TrackerReadScopeInsufficient(t *testing.T) {
	s := &ContainerServer{}
	ctx := kmsKeyTestCtx("alice", "member", "tracker:read")
	_, err := s.SubmitTrackerChange(ctx, &pb.SubmitTrackerChangeRequest{
		Username: "alice", Connection: "default", Issue: 1, Title: "t",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (tracker:read must not grant a write verb)", status.Code(err))
	}
}

func TestSubmitTrackerChange_NoTrackerStoreConfigured(t *testing.T) {
	s := &ContainerServer{}
	ctx := kmsKeyTestCtx("alice", "member", "tracker:write")
	_, err := s.SubmitTrackerChange(ctx, &pb.SubmitTrackerChangeRequest{
		Username: "alice", Connection: "default", Issue: 1, Title: "t",
	})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %v, want Unavailable", status.Code(err))
	}
}

func TestSubmitTrackerChange_RejectsMissingIssue(t *testing.T) {
	s := &ContainerServer{trackerStore: mustTestTrackerStore(t)}
	ctx := kmsKeyTestCtx("alice", "member", "tracker:write")
	_, err := s.SubmitTrackerChange(ctx, &pb.SubmitTrackerChangeRequest{
		Username: "alice", Connection: "default", Title: "t",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument (missing issue)", status.Code(err))
	}
}

func TestSubmitTrackerChange_RejectsMissingTitle(t *testing.T) {
	s := &ContainerServer{trackerStore: mustTestTrackerStore(t)}
	ctx := kmsKeyTestCtx("alice", "member", "tracker:write")
	_, err := s.SubmitTrackerChange(ctx, &pb.SubmitTrackerChangeRequest{
		Username: "alice", Connection: "default", Issue: 1,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument (missing title)", status.Code(err))
	}
}

func TestSubmitTrackerChange_CrossTenantDenied(t *testing.T) {
	s := &ContainerServer{trackerStore: mustTestTrackerStore(t)}
	ctx := kmsKeyTestCtx("mallory", "member", "tracker:write")
	_, err := s.SubmitTrackerChange(ctx, &pb.SubmitTrackerChangeRequest{
		Username: "alice", Connection: "default", Issue: 1, Title: "t",
	})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (cross-tenant)", status.Code(err))
	}
}

// validRunInfo is the runlease.Info a well-formed run-scoped submit
// resolves against — same run id setUpWriterConnection's context
// already carries ("run-abc123"), matching a run WITH a git_source.
func validRunInfo() runlease.Info {
	return runlease.Info{
		SkillID:   "code-review",
		Box:       "agent-box-1",
		GitCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Workspace: "/workspace/runs/run-abc123",
		GitRef:    "main",
	}
}

// setUpSubmitConnection layers #1923's box/pusher wiring and a
// registered run onto setUpWriterConnection's tracker connection setup.
// info nil means "don't register the run at all" (RunNotFound case).
func setUpSubmitConnection(t *testing.T, user string, provider *fakeWriterProvider, box *fakeSubmitBox, pusher *fakeSubmitPusher, info *runlease.Info) (*ContainerServer, context.Context) {
	t.Helper()
	s, ctx := setUpWriterConnection(t, user, provider)
	reg := runlease.NewRegistry()
	if info != nil {
		reg.Register("run-abc123", *info)
	}
	s.runRegistry = reg
	// A nil *fakeSubmitBox/*fakeSubmitPusher assigned into these
	// interface-typed fields would be a NON-nil interface wrapping a
	// nil pointer (the same typed-nil pitfall boxRunnerForSubmit's own
	// doc comment warns about in production) — panicking the first
	// time a method is called on it instead of falling through to the
	// "not configured" checks these fields exist to test. Guard here so
	// passing nil really means "leave it unset".
	if box != nil {
