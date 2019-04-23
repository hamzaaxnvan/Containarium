//go:build zfs

// Package zfspool builds throwaway ZFS pools for integration tests (#1200).
//
// Kept out of normal builds by the `zfs` build tag, and out of production
// code entirely: nothing here is imported by anything that ships.
//
// Pools are file-backed — a sparse image under the test's temp dir, no real
// disks — and named after the test binary's PID so they can never collide
// with a pool on the host. They are destroyed on teardown, including when
// the test fails.
package zfspool

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// vdevSize is comfortably above ZFS's 64 MiB minimum.
const vdevSize = 512 << 20

// Require skips the test unless a usable ZFS is present.
//
// Skips rather than fails on purpose: a developer's laptop or a container
// dev box legitimately has no ZFS. The CI lane that exists to demonstrate
// the encryption claim asserts the environment separately and fails on a
// skip, so this cannot quietly turn that lane into a no-op.
func Require(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("ZFS pool operations need root; run with sudo -E")
	}
	for _, bin := range []string{"zfs", "zpool"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH; install zfsutils-linux", bin)
		}
	}
	if _, err := os.Stat("/dev/zfs"); err != nil {
		t.Skipf("/dev/zfs is absent, so the kernel module cannot be driven: %v", err)
	}
}

// Run executes a command and fails the test on error.
func Run(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// Pool is a throwaway file-backed pool.
type Pool struct {
	Name  string // the ZFS pool name
	Mount string // its mountpoint
	Image string // the backing file
}

// New creates a pool and registers its teardown.
func New(t *testing.T) *Pool {
	t.Helper()
	dir := t.TempDir()
	p := &Pool{
		Name:  fmt.Sprintf("containarium-zfstest-%d", os.Getpid()),
		Mount: filepath.Join(dir, "mnt"),
		Image: filepath.Join(dir, "vdev.img"),
	}

	if err := os.MkdirAll(p.Mount, 0o755); err != nil {
		t.Fatalf("mkdir mountpoint: %v", err)
	}
	f, err := os.Create(p.Image)
