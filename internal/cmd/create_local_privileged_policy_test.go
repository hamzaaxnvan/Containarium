//go:build !containarium_client

package cmd

import "testing"

func TestLocalPodmanPrivileged_Disabled(t *testing.T) {
	t.Setenv("CONTAINARIUM_PRIVILEGED_PODMAN_POLICY", "disabled")

	got, err := localPodmanPrivileged(true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got {
		t.Fatalf("got EnablePodmanPrivileged = %v; want false", got)
	}
}
