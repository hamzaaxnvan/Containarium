//go:build !containarium_client

package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/pkg/core/container"
)

func getLabelsLocal(username string) (map[string]string, error) {
	mgr, err := container.New()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Incus: %w (is Incus running?)", err)
	}

	containerName := username + "-container"

	// Check if container exists
	if !mgr.ContainerExists(containerName) {
		return nil, fmt.Errorf("container %q does not exist", containerName)
	}

