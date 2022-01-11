//go:build !containarium_client

package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/pkg/core/container"
)

func runResizeLocal(username, containerName string) error {
	// Create container manager
	mgr, err := container.New()
	if err != nil {
		return fmt.Errorf("failed to connect to Incus: %w", err)
	}

	// Resize resources
	if err := mgr.Resize(containerName, newCPU, newMemory, newDisk, verbose); err != nil {
		return fmt.Errorf("failed to resize container: %w", err)
	}
