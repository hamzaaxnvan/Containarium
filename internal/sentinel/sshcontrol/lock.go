//go:build !windows

package sshcontrol

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)
