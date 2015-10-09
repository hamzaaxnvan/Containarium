package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// statusServer builds a server whose box reports the given level, whether the
// run user exists, and the sign-in probe's answer. calls collects every script
// run as root or as a user, so a test can prove the status check mutates nothing.
func statusServer(t *testing.T, level, existsOut, probeOut string, probeErr error) (*ContainerServer, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	record := func(s string) { mu.Lock(); calls = append(calls, s); mu.Unlock() }
	return &ContainerServer{
		boxRunLogDir:       t.TempDir(),
		boxRunCollaborator: func(owner, subject string) (bool, error) { return false, nil },
		boxRunToolAccess:   func(context.Context, string) (string, error) { return level, nil },
		boxRunExec: func(box, script string) (string, error) {
			record(script)
			if script == boxRunUserExistsScript() {
				return existsOut, nil
			}
			return "", errors.New("the status check ran a root script it must not: " + script)
		},
		boxRunStartExec: func(box, username, script string) (string, error) {
			record(script)
			if script == signInProbeScript(t) {
				if username != boxRunUnprivilegedUser {
					t.Errorf("sign-in probed as %q, want the run user", username)
				}
				return probeOut, probeErr
			}
			return "", errors.New("the status check ran a user script it must not: " + script)
		},
	}, &calls
}

func TestGetBoxRunUserStatus(t *testing.T) {
	tests := []struct {
		name       string
		level      string
		exists     string
		probe      string
		wantRec    bool
		wantExists bool
		wantState  pb.CodeCredentialSource
	}{
		{name: "no level recorded: run-user fields do not apply", wantState: pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_UNSPECIFIED},
		{name: "recorded, run user not created yet, is what a run would refuse on", level: "build_tools", exists: "no\n", wantRec: true, wantState: pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_NONE},
		{name: "recorded, never signed in", level: "none", exists: "yes\n", probe: "none\n", wantRec: true, wantExists: true, wantState: pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_NONE},
		{name: "recorded, signed in", level: "full_sudo", exists: "yes\n", probe: "interactive\n", wantRec: true, wantExists: true, wantState: pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_INTERACTIVE},
		{name: "recorded, api key", level: "build_tools", exists: "yes\n", probe: "api-key\n", wantRec: true, wantExists: true, wantState: pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_API_KEY},
		{name: "recorded, sign-in expired", level: "build_tools", exists: "yes\n", probe: "expired\n", wantRec: true, wantExists: true, wantState: pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_EXPIRED},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
