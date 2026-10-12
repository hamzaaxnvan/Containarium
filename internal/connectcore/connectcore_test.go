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
	}
}

// TestBuildTarget_Overrides — explicit user / host win over everything the
// daemon reported.
func TestBuildTarget_Overrides(t *testing.T) {
	c := &Container{Username: "cld-x", SshHost: "region-a.example.com"}
	c.Network.IpAddress = "10.0.0.5"
	got, err := BuildTarget(c, "root", "bastion.example.org", 2222)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.User != "root" || got.Host != "bastion.example.org" || got.Port != 2222 {
		t.Fatalf("got %+v, want root@bastion.example.org:2222", got)
	}
}

// TestBuildTarget_NoHostNoIP — a box still being placed has neither
// ssh_host nor IP; surface an actionable error rather than a broken target.
func TestBuildTarget_NoHostNoIP(t *testing.T) {
	c := &Container{Username: "alice"}
	if _, err := BuildTarget(c, "", "", 22); err == nil {
		t.Fatal("expected error when no ssh_host and no IP, got nil")
	}
}

func TestBuildTarget_NoUser(t *testing.T) {
	c := &Container{SshHost: "region-a.example.com"}
	if _, err := BuildTarget(c, "", "", 22); err == nil {
		t.Fatal("expected error when no username and no user override, got nil")
	}
}

// TestBuildSSHArgs — managed-key pinning, accept-new, non-default port,
// and the one-shot command tail.
func TestBuildSSHArgs(t *testing.T) {
	t.Run("interactive default port", func(t *testing.T) {
		args := BuildSSHArgs(Target{User: "cld-x", Host: "h.example.com", Port: 22}, "/k/id", "")
		joined := strings.Join(args, " ")
		for _, want := range []string{"IdentitiesOnly=yes", "StrictHostKeyChecking=accept-new", "-i /k/id", "cld-x@h.example.com"} {
			if !strings.Contains(joined, want) {
				t.Errorf("args %q missing %q", joined, want)
			}
		}
		if strings.Contains(joined, "-p ") {
			t.Errorf("default port 22 should not emit -p: %q", joined)
		}
	})
	t.Run("exec + custom port", func(t *testing.T) {
		args := BuildSSHArgs(Target{User: "alice", Host: "10.0.0.5", Port: 2222}, "/k/id", "make build")
		joined := strings.Join(args, " ")
		for _, want := range []string{"-p 2222", "alice@10.0.0.5", "make build"} {
			if !strings.Contains(joined, want) {
				t.Errorf("args %q missing %q", joined, want)
			}
		}
		// The command must be the LAST arg (ssh runs the tail remotely).
		if args[len(args)-1] != "make build" {
			t.Errorf("last arg = %q, want the remote command", args[len(args)-1])
		}
	})
}
