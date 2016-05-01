package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// platformMCPFixturePath is the fixture BOTH this suite and agent-runtime's
// seed.test.ts pin: the daemon writes exactly this shape and the runtime
// parses exactly this shape, so a change to either side that isn't mirrored
// fails a test on the other. See docs/architecture/agent-tracker-broker.md
// ("daemon <-> in-box runtime" contract row).
const platformMCPFixturePath = "../../agent-runtime/fixtures/platform_mcp.json"

func TestPlatformMCPSeed_MatchesSharedFixture(t *testing.T) {
	want, err := os.ReadFile(platformMCPFixturePath)
	if err != nil {
		t.Fatalf("read shared fixture: %v", err)
	}
	got, err := json.Marshal(platformMCPSeed{
		Command:   "env",
		Args:      []string{platformMCPAllowInsecureEnv + "=true", "mcp-server"},
		ServerURL: "http://10.9.8.1:8080",
		TokenFile: "/etc/containarium/agent/runs/run-1/token",
		Tools:     "tracker_*",
	})
	if err != nil {
		t.Fatal(err)
	}
	var a, b map[string]any
	if err := json.Unmarshal(want, &a); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	if err := json.Unmarshal(got, &b); err != nil {
		t.Fatal(err)
	}
	wa, _ := json.Marshal(a)
	wb, _ := json.Marshal(b)
	if string(wa) != string(wb) {
		t.Errorf("daemon shape drifted from the shared fixture\n daemon: %s\nfixture: %s", wb, wa)
	}
}

func TestPlatformMCPSeedScript_OnlyWhenRunIsBoundToAConnection(t *testing.T) {
	if _, ok := platformMCPSeedScript("/seed", 8080, ""); ok {
		t.Error("script produced for a run with no tracker connection — an unbound run must get no tracker tools")
	}
	if _, ok := platformMCPSeedScript("/seed", 0, "default"); ok {
		t.Error("script produced with no known daemon port — the URL would be unusable")
	}
	if _, ok := platformMCPSeedScript("/seed", 8080, "default"); !ok {
		t.Error("no script for a bound run with a known port")
