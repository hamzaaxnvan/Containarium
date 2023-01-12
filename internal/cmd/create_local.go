//go:build !containarium_client

package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/internal/server"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// containerExistsLocal is runCreate's --force existence check in local mode
// (Incus ContainerExists on the container's fully-qualified name).
func containerExistsLocal(username string) (bool, error) {
	mgr, err := container.New()
	if err != nil {
		return false, fmt.Errorf("failed to connect to Incus: %w", err)
	}
	containerName := username + "-container"
	return mgr.ContainerExists(containerName), nil
}

// createJumpServerAccountLocal creates the proxy-only SSH jump-server
// account for a new local-mode container. Only called when a key was
// provided (a keyless service tenant has no SSH path, so there's no jump
// account to seed).
func createJumpServerAccountLocal(username, sshKey string, verbose bool) error {
	if err := container.CreateJumpServerAccount(username, sshKey, verbose); err != nil {
		return fmt.Errorf("failed to create jump server account: %w\nNote: This command must be run with sudo/root privileges", err)
	}
	return nil
}

// cleanupJumpServerAccountLocal best-effort removes the jump-server account
// created by createJumpServerAccountLocal when the subsequent createLocal
// call fails. Mirrors the original inline `_ = container.DeleteJumpServerAccount(username, false)`.
func cleanupJumpServerAccountLocal(username string) {
	_ = container.DeleteJumpServerAccount(username, false)
}

func localPodmanPrivileged(enablePodman bool) (bool, error) {
	if !enablePodman {
		return false, nil
	}

	// Direct Incus access is root-equivalent; apply policy as the system principal.
	baseCtx := context.Background()
	ctx := auth.ContextWithSystemIdentity(baseCtx)
	return server.AuthorizePrivilegedPodman(ctx)
}

// createLocal creates a container using local Incus daemon
func createLocal(username, image, cpu, memory, disk, staticIP string, sshKeys []string, labelMap map[string]string, enablePodman bool, stack string, gpus []string, osType pb.OSType, isolation pb.IsolationType, monitoring bool, git client.GitSourceOpts, ttlSeconds int64, idleStopMinutes int32, deleteAfterStoppedSeconds int64, _ string) (*incus.ContainerInfo, error) {
	enablePodmanPrivileged, err := localPodmanPrivileged(enablePodman)
	if err != nil {
		return nil, err
	}

	mgr, err := container.New()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Incus: %w (is Incus running?)", err)
	}

	opts := container.CreateOptions{
		Username:               username,
		Image:                  image,
		CPU:                    cpu,
		Memory:                 memory,
		Disk:                   disk,
		GPUs:                   gpus,
		StaticIP:               staticIP,
		SSHKeys:                sshKeys,
		Labels:                 labelMap,
		EnablePodman:           enablePodman,
		EnablePodmanPrivileged: enablePodmanPrivileged, // Enable privileged mode for proper Podman-in-LXC
