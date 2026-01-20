//go:build !windows

package hostcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultRecoveryDir is where the daemon writes containarium-recovery.yaml.
// The file exists to survive instance recreation, so it is only useful if
// this path is backed by a disk that outlives the instance.
const DefaultRecoveryDir = "/mnt/incus-data"

// IsDurable reports whether path is its own mount point — i.e. backed by
// a filesystem mounted there, rather than being a plain directory on some
// ancestor's volume.
//
// This is the distinction os.Stat cannot make, and it is the whole of
// #1154: the daemon gated its recovery-config write on the directory
// merely existing. Any host where /mnt/incus-data existed for any reason
// — a leftover mkdir from the startup script, a partial migration — was
