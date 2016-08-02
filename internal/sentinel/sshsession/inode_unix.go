//go:build !windows

package sshsession

import (
	"os"
	"syscall"
)

