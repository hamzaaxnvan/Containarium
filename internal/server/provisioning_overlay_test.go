package server

import (
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// mkListEntry builds the minimal local list entry the overlay operates on.
func mkListEntry(username string, state pb.ContainerState) *pb.Container {
	return &pb.Container{
		Name:     username + "-container",
		Username: username,
		State:    state,
	}
}

func stateOf(t *testing.T, out []*pb.Container, username string) pb.ContainerState {
	t.Helper()
	for _, c := range out {
		if c.Username == username {
			return c.State
		}
	}
	t.Fatalf("container for %q not in overlay output", username)
	return pb.ContainerState_CONTAINER_STATE_UNSPECIFIED
}

func has(out []*pb.Container, username string) bool {
	for _, c := range out {
		if c.Username == username {
			return true
		}
	}
	return false
}

// TestApplyProvisioningOverlay_ReplacesRawRunning is the #1036 core case: a
// box mid-provisioning reads RUNNING from incus the moment it boots, minutes
// before SSH works — list must report the pending state instead, exactly as
// GetContainer already does.
func TestApplyProvisioningOverlay_ReplacesRawRunning(t *testing.T) {
	local := []*pb.Container{
		mkListEntry("alice", pb.ContainerState_CONTAINER_STATE_RUNNING),
		mkListEntry("bob", pb.ContainerState_CONTAINER_STATE_RUNNING),
	}
	pending := map[string]pb.ContainerState{
		"alice": pb.ContainerState_CONTAINER_STATE_PROVISIONING,
	}

	out := applyProvisioningOverlay(local, pending, "", pb.ContainerState_CONTAINER_STATE_UNSPECIFIED, false, "ssh.example.com")

	if got := stateOf(t, out, "alice"); got != pb.ContainerState_CONTAINER_STATE_PROVISIONING {
		t.Fatalf("alice: got %v, want PROVISIONING (raw incus RUNNING must not leak mid-provisioning)", got)
	}
	if got := stateOf(t, out, "bob"); got != pb.ContainerState_CONTAINER_STATE_RUNNING {
