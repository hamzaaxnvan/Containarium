//go:build linux

package hostcheck

import (
	"errors"
	"fmt"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/features"
	"golang.org/x/sys/unix"
)

func defaultEgressPrereqProbes() egressPrereqProbes {
	return egressPrereqProbes{
		kernelRelease: func() (string, error) {
			var u unix.Utsname
			if err := unix.Uname(&u); err != nil {
				return "", err
			}
