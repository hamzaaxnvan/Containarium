package mcp

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSaveEphemeralPrivateKey_WritesFileWithCorrectMode(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CONTAINARIUM_KEYS_DIR", tmp)

	keyText := []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nfakekeybytes\n-----END OPENSSH PRIVATE KEY-----\n")

	got, err := saveEphemeralPrivateKey("alice", keyText)
	if err != nil {
		t.Fatalf("saveEphemeralPrivateKey err = %v", err)
	}
	if got != filepath.Join(tmp, "alice") {
		t.Errorf("path = %q, want %q", got, filepath.Join(tmp, "alice"))
	}

	contents, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("readback err = %v", err)
	}
	if string(contents) != string(keyText) {
		t.Errorf("file contents differ:\nwant %q\ngot  %q", keyText, contents)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(got)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("file mode = %o, want 0600 (the agent's later ssh -i will reject otherwise)", mode)
		}
	}
