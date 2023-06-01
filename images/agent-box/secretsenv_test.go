// Package agentbox holds tests for the agent-box image's shell helpers.
//
// The helpers are shell, not Go, but they are as load-bearing as any code
// here: secrets-env.sh is what turns a mounted secret into the session
// environment (#1190), and a mistake in it is either a secret that never
// arrives or an environment a tenant can corrupt. Running it under `sh` in a
// test is the only way to check either without building the image.
package agentbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runWithSecrets writes the given files into a fake secrets mount, sources
// the snippet against it, and returns the resulting environment.
func runWithSecrets(t *testing.T, files map[string]string) map[string]string {
	t.Helper()

	dir := t.TempDir()
	mount := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(mount, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(mount, name), []byte(content), 0o400); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	script, err := os.ReadFile("secrets-env.sh")
	if err != nil {
		t.Fatalf("read snippet: %v", err)
	}
	// Point the snippet at the fake mount. The path is a literal in the
	// snippet because the real one is fixed by the pod spec.
	body := strings.ReplaceAll(string(script), "/run/secrets", mount)

	// env -0 (NUL-separated), not plain env: splitting on newlines cannot
	// distinguish "value" from "value\n", so a test for the trailing-newline
	// strip would pass either way.
	out, err := exec.Command("sh", "-c", body+"\nenv -0").Output()
	if err != nil {
		t.Fatalf("run snippet: %v", err)
	}

	env := map[string]string{}
	for _, entry := range strings.Split(string(out), "\x00") {
		if k, v, ok := strings.Cut(entry, "="); ok {
			env[k] = v
		}
	}
	return env
}

func TestSecretsEnv_ExportsEachSecret(t *testing.T) {
	env := runWithSecrets(t, map[string]string{
		"API_TOKEN": "s3cret",
		"DB_URL":    "postgres://host/db",
