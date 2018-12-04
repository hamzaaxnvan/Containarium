// Package toolchain describes the host-managed, shared build tools (#3164).
package toolchain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HostRoot is also the mount path in a box. Mount the parent, never current:
// switching the host symlink must be visible without replacing the bind mount.
const HostRoot = "/opt/containarium/toolchains"

const DeviceName = "toolchains"

var Tools = []string{"go", "gofmt", "node", "npm", "npx", "pnpm", "buf", "protoc", "staticcheck"}

// Available distinguishes hosts without a toolchain release from installed ones.
func Available(root string) bool {
	info, err := os.Stat(filepath.Join(root, "current", "manifest.json"))
	return err == nil && info.Mode().IsRegular()
}
