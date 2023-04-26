package k8s

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const nsChartDir = "../../../../charts/containarium-k8s"

// nsRender renders the chart with the walkthrough's flags (jwt secret, upstream
// key secret and public key) plus extra args, and returns the manifest.
func nsRender(t *testing.T, chart string, extra ...string) string {
	t.Helper()
	helmPath, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not found on PATH, skipping chart-rendering test")
	}
	pubKey := filepath.Join(t.TempDir(), "sshpiper_upstream.pub")
	if err := os.WriteFile(pubKey, []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAItest test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"template", "containarium", chart,
		"--set", "daemon.jwtSecret=deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdead",
		"--set", "gateway.upstreamKeySecret=sshpiper-upstream-key",
		"--set-file", "gateway.upstreamPublicKey=" + pubKey}, extra...)
	out, err := exec.Command(helmPath, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	return string(out)
}

// nsDoc returns the rendered gateway Namespace document, or "".
func nsDoc(manifest string) string {
	for _, doc := range strings.Split(manifest, "\n---\n") {
		if strings.Contains(doc, "kind: Namespace\n") {
