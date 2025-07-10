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
