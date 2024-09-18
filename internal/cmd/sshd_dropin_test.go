//go:build !windows

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Guards over the sshd settings setup-ssh-container-proxy.sh installs (#1137).
//
// The incident shape: the script wrote PrintMotd/PrintLastLog inside a
// `Match` block. Those directives are not permitted inside Match, so the
// whole sshd config became unparseable and sshd refused to start on its next
// restart — taking SSH to the backend host down, and with it every box on
// that backend, since sshpiper's upstream is that host's sshd.
//
// Everything else kept running and the backend still reported healthy, so
// nothing surfaced the outage until someone tried to connect; recovery
// needed the machine's console. The trigger was whatever restarted sshd
// next, in practice an unattended-upgrades openssh update at an arbitrary
// later time — which is why this must be caught at build time and not left
// to "it worked when we ran it".

const sshProxyScript = "../../scripts/setup-ssh-container-proxy.sh"

func readSSHProxyScript(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(sshProxyScript)
	if err != nil {
		t.Fatalf("read %s: %v", sshProxyScript, err)
	}
	return string(b)
}

// sshdConfigBlocks returns the SSHDEOF heredoc bodies the script writes into
// sshd configuration — the exact text that lands on a host.
