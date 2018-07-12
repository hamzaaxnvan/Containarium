//go:build darwin

package server

import (
	"os"
	"syscall"
)

// exeIdentityOf: see exec_hasher_identity_linux.go. Darwin builds run the
