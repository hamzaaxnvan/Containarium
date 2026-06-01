package connectcore

import (
	"strings"
	"testing"
)

func TestIsRunning(t *testing.T) {
	cases := map[string]bool{
		"CONTAINER_STATE_RUNNING":      true,
		"running":                      true,
		"Running":                      true,
		"CONTAINER_STATE_STOPPED":      false,
		"CONTAINER_STATE_PROVISIONING": false,
		"":                             false,
	}
	for state, want := range cases {
		if got := IsRunning(state); got != want {
			t.Errorf("IsRunning(%q) = %v, want %v", state, got, want)
		}
	}
}

func TestPrettyState(t *testing.T) {
	cases := map[string]string{
		"CONTAINER_STATE_RUNNING":      "running",
		"CONTAINER_STATE_PROVISIONING": "provisioning",
		"":                             "unknown",
	}
	for in, want := range cases {
		if got := PrettyState(in); got != want {
			t.Errorf("PrettyState(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBuildTarget_PrefersSSHHost — the daemon-owned ssh_host wins over the
// IP, and username supplies the SSH user (the #452 contract:
// username@ssh_host).
func TestBuildTarget_PrefersSSHHost(t *testing.T) {
	c := &Container{Username: "cld-a7b3c1d2", SshHost: "region-a.example.com"}
	c.Network.IpAddress = "10.0.0.5"
	got, err := BuildTarget(c, "", "", 22)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.User != "cld-a7b3c1d2" || got.Host != "region-a.example.com" || got.Port != 22 {
		t.Fatalf("got %+v, want cld-a7b3c1d2@region-a.example.com:22", got)
	}
}

// TestBuildTarget_FallsBackToIP — direct mode: no ssh_host, so the
// container IP is the host. This is the pre-#452 / self-host path.
func TestBuildTarget_FallsBackToIP(t *testing.T) {
	c := &Container{Username: "alice"}
	c.Network.IpAddress = "10.0.0.5"
	got, err := BuildTarget(c, "", "", 22)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Host != "10.0.0.5" {
		t.Fatalf("host = %q, want 10.0.0.5 (IP fallback)", got.Host)
