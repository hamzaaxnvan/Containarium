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
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeTool(t, bin, "go", "v1", "")
	if err := exec.Command("stat", "-c", "%s", filepath.Join(bin, "go")).Run(); err != nil {
		t.Skip("this stat has no -c (BSD/macOS): the fingerprint degrades to the path alone here; boxes are Linux")
	}
	env := probeEnv(t, root, []string{bin})
	probe := func() string { return fields(runShell(t, env, boxRunToolStartProbeFn()+"; __probe_start"))["go"][2] }
	a, b := probe(), probe()
	if a != b {
		t.Errorf("the fingerprint must be stable: %q then %q", a, b)
	}
	writeFakeTool(t, bin, "go", "a-much-longer-replacement-version-string", "")
	if c := probe(); c == a {
		t.Error("a replaced binary must change the fingerprint")
	}
}

// The END probe runs each present allowlisted tool's version command, reports the location as a
// class only, and never executes a binary that is not on the allowlist.
func TestBoxRunToolEndProbe_ReportsOnlyTheAllowlistAndNeverRunsAnythingElse(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "runhome")
	homeBin := filepath.Join(home, ".local", "bin")
	sysBin := filepath.Join(root, "sysbin")
	for _, d := range []string{homeBin, sysBin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(root, "EXECUTED-NON-ALLOWLISTED")
	writeFakeTool(t, homeBin, "buf", "1.99.0", "")
	writeFakeTool(t, sysBin, "go", "go version go1.99.0 linux/amd64", "")
	writeFakeTool(t, homeBin, "evil-tool", "pwned", marker)
	writeFakeTool(t, sysBin, "backdoor", "pwned", marker)

	out := runShell(t, probeEnv(t, home, []string{homeBin, sysBin}), boxRunToolEndProbeFn()+"; __probe_end "+shellSingleQuote(filepath.Join(t.TempDir(), "p")))
	got := fields(out)
	if f := got["buf"]; len(f) != 5 || f[0] != "E" || f[2] != "1.99.0" || f[3] != "home" || f[4] == "" {
		t.Errorf("buf line = %v, want E / buf / 1.99.0 / home / fingerprint", f)
	}
	if f := got["go"]; len(f) != 5 || f[2] != "go version go1.99.0 linux/amd64" || f[3] != "system" {
		t.Errorf("go line = %v, want its version and location system", f)
	}
	if got["evil-tool"] != nil || got["backdoor"] != nil {
		t.Errorf("the probe reported a tool that is not on the allowlist: %v", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the probe EXECUTED a binary that is not on the allowlist")
	}
	if strings.Contains(out, home) || strings.Contains(out, sysBin) {
		t.Errorf("the probe must report a location class, never a path:\n%s", out)
	}
}

// A tool whose version output is long or full of control characters is reduced to one
// printable, capped line (the value ends up in a public pull request).
func TestBoxRunToolEndProbe_SanitisesTheVersionLine(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("v1.2.3-", 40)
	body := "#!/bin/sh\nprintf '\\033[31m" + long + "\\033[0m\\tTAB\\nsecond line\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "node"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	f := fields(runShell(t, probeEnv(t, root, []string{bin}), boxRunToolEndProbeFn()+"; __probe_end "+shellSingleQuote(filepath.Join(t.TempDir(), "p"))))["node"]
	if len(f) != 5 {
		t.Fatalf("node line = %v, want exactly 5 tab-separated fields", f)
	}
	if len(f[2]) > boxRunToolVersionMax || strings.ContainsAny(f[2], "\x1b\n\t") || strings.Contains(f[2], "second line") {
		t.Errorf("version not sanitised: %q", f[2])
	}
}

// The END probe runs the version commands IN PARALLEL: the exit sidecar waits for it, so its total
// time must be about the slowest tool, not the sum. Three tools that each take 2 s must finish in
// well under the 6 s a sequential run needs (the bound is 5.2 s, to tolerate a loaded runner), and their lines still come out in allowlist order.
func TestBoxRunToolEndProbe_RunsVersionCommandsInParallelAndKeepsTheOrder(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go", "node", "buf"} {
		body := "#!/bin/sh\nsleep 2\necho " + name + "-slow\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	partsDir := t.TempDir()
	start := time.Now()
	out := runShell(t, probeEnv(t, root, []string{bin}), boxRunToolEndProbeFn()+"; __probe_end "+shellSingleQuote(filepath.Join(partsDir, "p")))
	if left, err := os.ReadDir(partsDir); err != nil || len(left) != 0 {
		t.Errorf("the per-tool part files must be removed after the probe, left: %v (%v)", left, err)
	}
	if took := time.Since(start); took > 5200*time.Millisecond {
		t.Errorf("three 2 s tools took %v: the version commands must run in parallel", took)
	}
	var order []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
