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
func sshdConfigBlocks(t *testing.T) []string {
	t.Helper()
	// Non-greedy so each heredoc is captured separately.
	re := regexp.MustCompile(`(?s)<< 'SSHDEOF'\n(.*?)\nSSHDEOF`)
	matches := re.FindAllStringSubmatch(readSSHProxyScript(t), -1)

	var blocks []string
	for _, m := range matches {
		// Only the sshd_config ones; the script has other heredocs.
		if strings.Contains(m[1], "PrintMotd") || strings.Contains(m[1], "PrintLastLog") {
			blocks = append(blocks, m[1])
		}
	}
	if len(blocks) == 0 {
		t.Fatal("found no sshd config blocks in the script — this guard would pass vacuously")
	}
	return blocks
}

// The cheap guard, and the one that runs everywhere: no Match block.
//
// PrintMotd/PrintLastLog inside Match is rejected wherever the file lives —
// the restriction is on Match, not on the drop-in directory. So "move it to
// the main sshd_config" is not a fix, and this check deliberately does not
// care which branch of the script wrote the block.
func TestSSHDDropInHasNoMatchBlock(t *testing.T) {
	for i, block := range sshdConfigBlocks(t) {
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "Match ") {
				t.Errorf("sshd config block %d contains a Match block:\n\t%s\n"+
					"PrintMotd/PrintLastLog are not permitted inside Match — this makes the "+
					"entire sshd config unparseable, so sshd refuses to start on its next "+
					"restart and SSH to the host (and every box on it) is lost (#1137).",
					i, strings.TrimSpace(line))
			}
		}
	}
}

// The real check: hand the block to the actual sshd parser.
//
// A hand-rolled "does it look right" assertion would have missed the
// original bug too — the block looked entirely reasonable. Only the parser
