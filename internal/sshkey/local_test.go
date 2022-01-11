package sshkey

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// withTempHome returns a tempdir that callers should pass as
// LocateOpts.HomeDir. We do not Setenv HOME here because the only
// production caller paths through the LocateOpts.HomeDir override —
// keeping the test hermetic.
func withTempHome(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// seedPubKey drops a placeholder public key file at ~/.ssh/<name>
// under home. The contents are a freshly-generated ed25519 key, so
// ssh.ParseAuthorizedKey accepts them; we reuse Generate to keep the
// helper short.
func seedPubKey(t *testing.T, home, name string) string {
	t.Helper()
	// Use Generate against a fresh tempdir to get a syntactically-valid
	// key, then copy the .pub into the target home.
	stage := t.TempDir()
	_, pub, err := Generate(LocateOpts{HomeDir: stage}, false)
	if err != nil {
		t.Fatalf("seed: Generate: %v", err)
	}
	dir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("seed: mkdir: %v", err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(pub+"\n"), 0o644); err != nil {
		t.Fatalf("seed: write: %v", err)
	}
	return p
}

func TestGenerate_WritesBothKeysWithCorrectModes(t *testing.T) {
	home := withTempHome(t)
	pubPath, pub, err := Generate(LocateOpts{HomeDir: home}, false)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if !strings.HasSuffix(pubPath, "/containarium_ed25519.pub") {
		t.Errorf("pubPath = %q, want suffix containarium_ed25519.pub", pubPath)
	}
	if !strings.HasPrefix(pub, "ssh-ed25519 ") {
		t.Errorf("pub = %q, want ssh-ed25519 prefix", pub)
	}

	priv := strings.TrimSuffix(pubPath, ".pub")
	if _, err := os.Stat(priv); err != nil {
		t.Fatalf("private key not written: %v", err)
	}

	// POSIX perm checks only — Windows has no notion of 0600.
	if runtime.GOOS == "windows" {
		return
	}
	if st, _ := os.Stat(priv); st.Mode().Perm() != 0o600 {
		t.Errorf("private mode = %o, want 0600", st.Mode().Perm())
	}
	if st, _ := os.Stat(pubPath); st.Mode().Perm() != 0o644 {
		t.Errorf("public mode = %o, want 0644", st.Mode().Perm())
	}
}

func TestGenerate_RefusesClobberWithoutForce(t *testing.T) {
	home := withTempHome(t)
	if _, _, err := Generate(LocateOpts{HomeDir: home}, false); err != nil {
		t.Fatalf("Generate first: %v", err)
	}
	_, _, err := Generate(LocateOpts{HomeDir: home}, false)
	if err == nil {
		t.Fatal("expected error on second Generate without force")
	}
	if !errors.Is(err, os.ErrExist) {
		t.Errorf("err = %v, want ErrExist", err)
	}
}

func TestGenerate_ForceOverwrites(t *testing.T) {
	home := withTempHome(t)
	_, pub1, err := Generate(LocateOpts{HomeDir: home}, false)
	if err != nil {
		t.Fatalf("Generate 1: %v", err)
	}
	_, pub2, err := Generate(LocateOpts{HomeDir: home}, true)
	if err != nil {
		t.Fatalf("Generate 2 (force): %v", err)
