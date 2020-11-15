//go:build linux

package server

import (
	"github.com/footprintai/containarium/internal/threatdetect"
	"golang.org/x/sys/unix"
)

// defaultUserHZ is USER_HZ on every Linux architecture Go supports; the
// daemon reads the real value from the auxiliary vector (AT_CLKTCK) and
// falls back to this.
const defaultUserHZ = 100

// userHZ is the tick rate /proc/<pid>/stat reports times in.
var userHZ = clockTicksPerSecond()

// clockTicksPerSecond reads AT_CLKTCK (what sysconf(_SC_CLK_TCK) returns)
// from the auxiliary vector, defaultUserHZ when it is absent.
func clockTicksPerSecond() uint64 {
	const atClkTck = 17 // AT_CLKTCK, <linux/auxvec.h>
	if av, err := unix.Auxv(); err == nil {
		for _, kv := range av {
			if kv[0] == atClkTck && kv[1] > 0 {
				return uint64(kv[1])
			}
		}
	}
