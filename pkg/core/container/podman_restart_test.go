package container

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus/incustest"
)

// captureExec returns a Manager whose backend records every Exec command.
func captureExec() (*Manager, *[][]string) {
	var calls [][]string
	mock := &incustest.MockBackend{
		ExecFunc: func(_ string, command []string) error {
			calls = append(calls, command)
			return nil
		},
	}
	return NewWithBackend(mock), &calls
}

func hasCall(calls [][]string, match func([]string) bool) bool {
	for _, c := range calls {
		if match(c) {
			return true
		}
	}
	return false
}

func TestEnablePodmanRestartDurability_RootfulAndRootless(t *testing.T) {
	m, calls := captureExec()
	m.enablePodmanRestartDurability("cld-test", "alice")

	// Rootful: system podman-restart.service enabled.
	if !hasCall(*calls, func(c []string) bool {
		return len(c) >= 3 && c[0] == "systemctl" && c[1] == "enable" && c[2] == "podman-restart.service"
