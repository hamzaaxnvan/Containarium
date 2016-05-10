package server

import (
	"context"
	"errors"
	"github.com/footprintai/containarium/internal/auth"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Per-workstream test file (NEW; not appended to a shared *_test.go).
// Drives ComposeAutostartServer through a fake IncusExecer so we
// exercise the envelope-parsing + proto-mapping paths without any
// real LXC.

type fakeExecer struct {
	gotContainer string
	gotCommand   []string

	stdout string
	stderr string
	err    error
}

func (f *fakeExecer) ExecWithOutput(containerName string, command []string) (string, string, error) {
	f.gotContainer = containerName
	f.gotCommand = append(f.gotCommand[:0], command...)
	return f.stdout, f.stderr, f.err
}

// These handlers gained tenant + scope guards in #1716, so every call needs an
// authenticated context. composeCallerCtx is alice acting on her OWN box with
// both scopes — the authorized case, so these tests keep exercising the
// command-building behaviour they were written for rather than the new gate.
// The gate itself is tested separately in authguard_writepath_test.go.
func composeCallerCtx() context.Context {
	return auth.ContextWithTestSubjectScopes(context.Background(), "alice",
		[]string{"user"}, []string{auth.ScopeContainersRead, auth.ScopeContainersWrite})
}

// composeAdminCtx reaches argument validation for requests that deliberately
// carry no username. Auth runs BEFORE validation on purpose (the convention
// SetTenantKMSKey states: an under-privileged caller must not get to probe
// argument handling), so a blank username would otherwise be refused by the
// tenant check before the validation under test ever runs.
func composeAdminCtx() context.Context {
	return auth.ContextWithTestSubjectScopes(context.Background(), "ops",
		[]string{auth.RoleAdmin}, []string{auth.ScopeContainersRead, auth.ScopeContainersWrite})
}

func TestExecAgentBox_BlankUsername_InvalidArg(t *testing.T) {
	s := NewComposeAutostartServer(&fakeExecer{})
	_, err := s.Discover(composeAdminCtx(), &pb.DiscoverRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument; err = %v", status.Code(err), err)
	}
}

func TestExecAgentBox_TransportError_Internal(t *testing.T) {
	f := &fakeExecer{err: errors.New("ssh connection refused"), stderr: "no route to host"}
	s := NewComposeAutostartServer(f)
	_, err := s.Status(composeCallerCtx(), &pb.StatusRequest{Username: "alice", Dir: "/srv"})
	if status.Code(err) != codes.Internal {
		t.Errorf("code = %v, want Internal", status.Code(err))
	}
	if !strings.Contains(err.Error(), "no route to host") {
		t.Errorf("err missing inlined stderr: %v", err)
	}
}

func TestExecAgentBox_MalformedEnvelope_Internal(t *testing.T) {
	f := &fakeExecer{stdout: "not json {{"}
	s := NewComposeAutostartServer(f)
	_, err := s.Status(composeCallerCtx(), &pb.StatusRequest{Username: "alice", Dir: "/srv"})
	if status.Code(err) != codes.Internal {
		t.Errorf("code = %v, want Internal (malformed envelope)", status.Code(err))
	}
}

func TestExecAgentBox_EnvelopeFalse_FailedPrecondition(t *testing.T) {
	f := &fakeExecer{stdout: `{"ok":false,"error":"no compose runtime found on PATH"}`}
	s := NewComposeAutostartServer(f)
	_, err := s.Status(composeCallerCtx(), &pb.StatusRequest{Username: "alice", Dir: "/srv"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition", status.Code(err))
	}
	if !strings.Contains(err.Error(), "no compose runtime") {
		t.Errorf("agent-box's error message not bubbled up: %v", err)
	}
}

func TestDiscover_BuildsCommandFromRequest(t *testing.T) {
	f := &fakeExecer{stdout: `{"ok":true,"result":{"stacks":[]}}`}
	s := NewComposeAutostartServer(f)
	_, err := s.Discover(composeCallerCtx(), &pb.DiscoverRequest{
		Username: "alice",
		Root:     "/home/alice",
		MaxDepth: 4,
		Skip:     []string{"node_modules", "vendor"},
		NoSkip:   true,
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if f.gotContainer != "alice-container" {
		t.Errorf("container = %q, want alice-container", f.gotContainer)
	}
	want := []string{"agent-box", "compose", "discover",
		"--root", "/home/alice",
		"--max-depth", "4",
		"--skip", "node_modules",
		"--skip", "vendor",
		"--no-skip",
	}
	if !equalCmd(f.gotCommand, want) {
		t.Errorf("command = %v\nwant %v", f.gotCommand, want)
	}
}

func TestDiscover_OmitsZeroValuedFlags(t *testing.T) {
	f := &fakeExecer{stdout: `{"ok":true,"result":{"stacks":[]}}`}
	s := NewComposeAutostartServer(f)
	_, err := s.Discover(composeCallerCtx(), &pb.DiscoverRequest{
		Username: "alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"agent-box", "compose", "discover"}
	if !equalCmd(f.gotCommand, want) {
		t.Errorf("command = %v, want %v (no flags when zero values)", f.gotCommand, want)
	}
}

func TestEnable_RequiresDir(t *testing.T) {
	s := NewComposeAutostartServer(&fakeExecer{})
	_, err := s.Enable(composeCallerCtx(), &pb.EnableRequest{Username: "alice"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestEnable_ForceFlagPropagates(t *testing.T) {
	f := &fakeExecer{stdout: `{"ok":true,"result":{"unit":"u","dir":"/d","compose_bin":"podman compose","already":false}}`}
	s := NewComposeAutostartServer(f)
	_, err := s.Enable(composeCallerCtx(), &pb.EnableRequest{
		Username: "alice",
		Dir:      "/srv/app",
		Force:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
