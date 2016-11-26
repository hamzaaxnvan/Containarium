//go:build !windows

package audit

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// bindToProcessGroup puts the command in its own process group and makes
