//go:build linux

package agentbox

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/coderun"
	"github.com/footprintai/containarium/internal/coderun/engine"
)

// approvalE2EEnv opts into TestApprovalEndToEnd. It runs the real Claude Code
// against a real model three times, so it needs a signed-in ~/.local/bin/claude
// and costs a few model calls; CI does not set it.
const approvalE2EEnv = "CONTAINARIUM_APPROVAL_E2E"

// TestApprovalEndToEnd runs the exact command `code run --approvals ask`
// dispatches, with this tree's agent-box on PATH, on a prompt that needs one
// gated command (touch outside the working directory):
//   - allow → exit 0 and the file exists
//   - deny  → the file is absent
//   - no answer → the request expires and the journal records it
func TestApprovalEndToEnd(t *testing.T) {
	if os.Getenv(approvalE2EEnv) != "1" {
		t.Skipf("set %s=1 to run against a real model (needs a signed-in ~/.local/bin/claude)", approvalE2EEnv)
	}
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".local/bin/claude")); err != nil {
		t.Skipf("no ~/.local/bin/claude: %v", err)
	}
	// The approval server's PATH puts these first; an installed agent-box there
	// would shadow the one built from this tree.
	for _, p := range []string{filepath.Join(home, ".local/bin/agent-box"), "/usr/local/bin/agent-box"} {
		if _, err := os.Stat(p); err == nil {
			t.Skipf("%s would shadow the agent-box built from this tree", p)
		}
	}

	bin := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(bin, "agent-box"), "github.com/footprintai/containarium/cmd/agent-box")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build agent-box: %v\n%s", err, out)
	}
	if err := os.MkdirAll(DefaultLogDir, 0o755); err != nil {
