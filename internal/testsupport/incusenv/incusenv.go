//go:build incus

package incusenv

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// SocketPath is where the daemon's own client looks for Incus. Kept here
// rather than reaching into incus.New's default so a test failure names the
// path the environment was checked at.
const SocketPath = "/var/lib/incus/unix.socket"

// Require returns a client connected to the local Incus, having checked that
// one is actually usable.
//
// What happens when it is not usable is DispositionFor's call: a skip on a
// machine that never had Incus, a failure in the lane that set RequireEnv.
// Either way the reason is spelled out — "which step, which error" is what
// #1332's timebox asks for if the environment turns out to be impossible.
func Require(t *testing.T) *incus.Client {
	t.Helper()

	if os.Geteuid() != 0 {
		unusable(t, "Incus operations need root; run with sudo -E")
		return nil
	}
	if _, err := os.Stat(SocketPath); err != nil {
		unusable(t, "no Incus socket at %s, so the daemon's create path cannot be driven: %v", SocketPath, err)
		return nil
	}

	client, err := incus.NewWithSocket(SocketPath)
	if err != nil {
		unusable(t, "cannot connect to Incus at %s: %v", SocketPath, err)
		return nil
