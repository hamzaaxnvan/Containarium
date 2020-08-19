//go:build containarium_client

package main

import (
	"fmt"
	"os"

	"github.com/footprintai/containarium/internal/cmd"
)

var (
	// Version is set by ldflags during build
	Version = "dev"
	// BuildTime is set by ldflags during build
	BuildTime = "unknown"
)

func main() {
	// Set version info for commands
	cmd.SetVersionInfo(Version, BuildTime)
