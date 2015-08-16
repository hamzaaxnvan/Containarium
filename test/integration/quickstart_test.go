package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestQuickstartLocalIncus is the daemon-level integration test for
// `containarium quickstart`. Unlike the hermetic orchestration test in
// internal/cmd (which fakes the daemon), this runs the REAL binary end to end
// against a running instance and asserts that a box is actually created and
// that the real SSH + agent-MCP config gets written.
//
// It self-skips unless you point it at a binary and (optionally) a server:
//
//	CONTAINARIUM_BIN=$(pwd)/bin/containarium \
//	CONTAINARIUM_SERVER=localhost:50051 \
//	  go test -v -run TestQuickstartLocalIncus ./test/integration/
//
// Auth/TLS flags your daemon needs (e.g. --insecure, --token) can be supplied
// via CONTAINARIUM_TEST_FLAGS (space-separated), appended to every CLI call.
// HOME is redirected to a temp dir so the test never touches your real
// ~/.ssh/config or ~/.claude.json.
func TestQuickstartLocalIncus(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	if os.Getenv("CONTAINARIUM_BIN") == "" {
		t.Skip("set CONTAINARIUM_BIN to the containarium binary to run this test")
	}

	server := getServerAddr(t)
	home := t.TempDir()
	const box = "qs-it-box"

	// Best-effort pre-clean (a prior run may have left the box) + teardown.
	_, _ = runQuickstartCLI(t, home, "delete", box, "--server", server, "--force")
	t.Cleanup(func() {
		_, _ = runQuickstartCLI(t, home, "delete", box, "--server", server, "--force")
	})

	// 1. quickstart wires everything. No --ssh-key → auto-provision a managed
	//    key; no --prompt → no expose/launch (those need a sentinel + a real
	//    agent, out of scope for a daemon-only integration test).
	out, err := runQuickstartCLI(t, home, "quickstart", box, "--server", server)
	require.NoError(t, err, "quickstart failed:\n%s", out)

