package container

import (
	"fmt"

	"github.com/footprintai/containarium/pkg/core/toolchain"
)

func (m *Manager) managedToolchainRoot() string {
	if m.toolchainRoot != "" {
		return m.toolchainRoot
	}
	return toolchain.HostRoot
}

// EnsureManagedToolchains lazily equips an existing box. No software is
// downloaded by a run: the operator must first install a host release.
func (m *Manager) EnsureManagedToolchains(username string) error {
