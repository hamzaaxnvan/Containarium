//go:build linux

package server

import (
	"errors"

	"golang.org/x/sys/unix"
)

// pidfdHandle signals through a pidfd (Linux 5.3+): the fd refers to the
// process that held the pid when it was opened, so a later pid reuse turns
// the signal into ESRCH instead of a kill of the new process.
type pidfdHandle struct{ fd int }

func (h pidfdHandle) Kill() error {
	err := unix.PidfdSendSignal(h.fd, unix.SIGKILL, nil, 0)
	if errors.Is(err, unix.ESRCH) {
		return errKillProcessGone
	}
