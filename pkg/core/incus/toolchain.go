package incus

import (
	"reflect"

	"github.com/footprintai/containarium/pkg/core/toolchain"
)

func toolchainDevice(root string) map[string]string {
	return map[string]string{"type": "disk", "source": root, "path": toolchain.HostRoot, "readonly": "true"}
}

// EnsureToolchainVolume hotplugs the shared parent directory on existing boxes.
// Update through the same ETag/ZFS-quota seam used for other device changes.
