//go:build !windows

package agentbox

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// Per-workstream test file (NEW; not appended to a shared *_test.go)
// per the Phase-1 lesson. Focused on the dispatcher + envelope shape
// — the underlying compose ops (discoverStacks, systemctlUser, etc.)
// touch real systemd/podman and aren't worth mocking; the existing
// MCP-handler tests in compose_test.go cover the inner-logic edges.

func TestRunComposeCLI_NoArgs(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := RunComposeCLI(nil, &out, &errBuf)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(errBuf.String(), "usage:") {
		t.Errorf("stderr missing usage line: %q", errBuf.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout should be empty on usage error, got: %q", out.String())
	}
}

func TestRunComposeCLI_UnknownVerb(t *testing.T) {
	var out, errBuf bytes.Buffer
