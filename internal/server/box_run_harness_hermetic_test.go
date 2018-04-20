//go:build !containarium_client

package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// slowToolShims puts executables named like the package managers and runtimes
// the toolchain probe runs (#2954) at the FRONT of PATH, each sleeping far longer
// than the probe's own 3s timeout. A CI runner has real ones, and the slow ones
// (npm, yarn, pnpm shims, a cold JVM) are exactly what held a run open (#2973).
func slowToolShims(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"yarn", "pnpm", "npm", "java", "node"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nsleep 30\necho 1.0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// #2973: the box-run tests must not depend on which tools the machine running
