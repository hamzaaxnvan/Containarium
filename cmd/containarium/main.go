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
