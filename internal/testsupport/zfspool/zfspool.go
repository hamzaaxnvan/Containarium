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

