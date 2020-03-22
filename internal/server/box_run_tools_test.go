package server

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// writeFakeTool writes an executable named name in dir that prints out (and, when marker
// is set, also touches it, so a test can tell the tool was executed).
func writeFakeTool(t *testing.T, dir, name, out, marker string) {
	t.Helper()
	body := "#!/bin/sh\n"
	if marker != "" {
		body += "touch '" + marker + "'\n"
	}
	body += "printf '%s\\n' '" + out + "'\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// probeEnv builds a HERMETIC environment for the probe: only the given fake tool
// directories plus symlinks to the plain utilities the probes use. No real git, python3,
// node, make or java is ever executed or even visible (on a developer machine or a CI
// runner those can be slow shims, and their presence would change what a test sees).
func probeEnv(t *testing.T, home string, pathDirs []string) []string {
	t.Helper()
	utils := t.TempDir()
	for _, u := range []string{"head", "tr", "cut", "timeout", "sleep", "date", "touch", "stat", "cksum", "printf", "cat", "sed", "ls", "rm"} {
		if p, err := exec.LookPath(u); err == nil {
			if err := os.Symlink(p, filepath.Join(utils, u)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return []string{"HOME=" + home, "PATH=" + strings.Join(append(append([]string{}, pathDirs...), utils), ":")}
}

func runShell(t *testing.T, env []string, script string) string {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe failed: %v\n%s\n--- script ---\n%s", err, out, script)
	}
	return string(out)
}

func fields(out string) map[string][]string {
	m := map[string][]string{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		f := strings.Split(line, "\t")
		if len(f) >= 3 {
			m[f[1]] = f
		}
	}
	return m
}

// The START probe resolves allowlisted tools and fingerprints their binaries, and it EXECUTES
// NOTHING: not the allowlisted tools (a fake one would touch a marker) and not anything else.
// That is what lets it run synchronously before the engine without ever delaying it.
func TestBoxRunToolStartProbe_ExecutesNothing(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "SOMETHING-WAS-EXECUTED")
	writeFakeTool(t, bin, "go", "go version go1.99.0", marker)
	writeFakeTool(t, bin, "buf", "1.99.0", marker)
	writeFakeTool(t, bin, "evil-tool", "pwned", marker) // not on the allowlist

	out := runShell(t, probeEnv(t, root, []string{bin}), boxRunToolStartProbeFn()+"; __probe_start")
	got := fields(out)
	for _, name := range []string{"go", "buf"} {
		f := got[name]
		if len(f) != 3 || f[0] != "S" || f[2] == "" {
			t.Errorf("%s start line = %v, want S / name / fingerprint", name, f)
		}
	}
	if got["evil-tool"] != nil {
		t.Error("the probe reported a tool that is not on the allowlist")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the START probe EXECUTED a binary; it must only resolve and fingerprint")
	}
	if strings.Contains(out, root) {
		t.Errorf("the probe must never print a path:\n%s", out)
	}
}

// The fingerprint tells a replaced or upgraded binary from an unchanged one without running
// it, and is stable across calls.
func TestBoxRunToolStartProbe_FingerprintTracksTheBinary(t *testing.T) {
	root := t.TempDir()
