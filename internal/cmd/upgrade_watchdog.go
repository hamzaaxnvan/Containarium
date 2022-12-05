//go:build !windows && !containarium_client

// The upgrade watchdog drives the daemon's post-upgrade auto-rollback, so it
// imports internal/server (which pulls in the Linux-only eBPF loader). The
// Windows build is client-only — gate this command out of it, matching daemon.go.

package cmd

import (
	"log"
	"time"
