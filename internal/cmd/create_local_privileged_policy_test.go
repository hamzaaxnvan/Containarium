//go:build !containarium_client

package cmd

import "testing"

func TestLocalPodmanPrivileged_Disabled(t *testing.T) {
	t.Setenv("CONTAINARIUM_PRIVILEGED_PODMAN_POLICY", "disabled")

