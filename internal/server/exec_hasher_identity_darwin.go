//go:build darwin

package server

import (
	"os"
	"syscall"
)

// exeIdentityOf: see exec_hasher_identity_linux.go. Darwin builds run the
// unit tests; the daemon itself runs on Linux.
func exeIdentityOf(fi os.FileInfo) (exeIdentity, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return exeIdentity{}, false
