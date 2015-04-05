package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// These pin the fail-fast validation that runs BEFORE newSandboxGRPCClient
// dials anything, so they need no server and no --server flag.

func TestRunSandboxExec_RequiresCommand(t *testing.T) {
	if err := runSandboxExec(sandboxExecCmd, []string{"sandbox-abc123"}); err == nil {
		t.Fatal("exec with no command should fail before dialing a server")
	}
}

func TestRunSandboxWriteFile_RequiresFrom(t *testing.T) {
	old := sandboxWriteFrom
	sandboxWriteFrom = ""
	defer func() { sandboxWriteFrom = old }()

	if err := runSandboxWriteFile(sandboxWriteFileCmd, []string{"sandbox-abc123", "/workspace/out.txt"}); err == nil {
		t.Fatal("write-file with no --from should fail before dialing a server")
	}
}

func TestRunSandboxWriteFile_MissingLocalFile(t *testing.T) {
	old := sandboxWriteFrom
