//go:build linux

package server

import (
	"os"
	"syscall"
)

// exeIdentityOf is the cache key of an opened executable (#2768): device,
// inode, size, mtime and ctime. ctime cannot be set from user space, so
