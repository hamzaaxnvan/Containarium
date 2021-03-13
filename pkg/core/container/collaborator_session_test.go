package container

import (
	"fmt"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// recordingBackend records the argv of every Exec issued into the container.
// It embeds incus.Backend so it satisfies the 28-method interface with nil
// method bodies for everything the test doesn't exercise; only Exec is real.
type recordingBackend struct {
	incus.Backend
	calls       [][]string
	userdelErr  error // injected failure for the userdel step
	userdelSeen bool
	// pkillExit / pkillErr are what ExecWithExitCode reports (#2522).
	pkillExit int
	pkillErr  error
}

func (r *recordingBackend) ExecWithExitCode(_ string, command []string) (string, string, int, error) {
	r.calls = append(r.calls, command)
	return "", "", r.pkillExit, r.pkillErr
}

func (r *recordingBackend) Exec(_ string, command []string) error {
	r.calls = append(r.calls, command)
	if len(command) > 0 && command[0] == "userdel" {
		r.userdelSeen = true
		return r.userdelErr
	}
	return nil
}

func (r *recordingBackend) index(cmd string) int {
	for i, c := range r.calls {
		if len(c) > 0 && c[0] == cmd {
			return i
		}
	}
	return -1
}

// TestRemoveCollaboratorUser_TerminatesSessionThenForceDeletes pins the #1177
// contract: revoking a collaborator kills the account's live session BEFORE
// deleting it, and the delete is forced (-f) so an in-use account can't block
// removal or survive re-loginnable.
func TestRemoveCollaboratorUser_TerminatesSessionThenForceDeletes(t *testing.T) {
	fake := &recordingBackend{}
