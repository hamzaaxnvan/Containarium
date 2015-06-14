package cluster

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite golden files")

// golden compares rendered bootstrap output against testdata — what
// executes inside a tenant's cluster VM changes only as a reviewable
// golden diff (`go test ./pkg/core/cluster/ -update` to regenerate).
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with -update to create): %v", path, err)
	}
	if got != string(want) {
		t.Fatalf("rendered output diverges from %s.\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

func TestRenderServerScript(t *testing.T) {
	got := RenderServerScript(ServerBootstrap{TLSSANs: []string{"203.0.113.10", "10.166.11.5"}})
	golden(t, "server.sh.golden", got)

	// Behavioral pins independent of the golden bytes.
	for _, must := range []string{
		"--disable traefik",
		"--node-taint node-role.kubernetes.io/control-plane=:NoSchedule",
		"--write-kubeconfig-mode 0600",
		"--tls-san 10.166.11.5",
		"--tls-san 203.0.113.10",
	} {
		if !strings.Contains(got, must) {
			t.Fatalf("server script missing %q", must)
		}
