//go:build containarium_client

package cmd

import (
	"testing"

	"github.com/footprintai/containarium/internal/credentials"
)

// writeDefaultServer writes a minimal credentials.json under a temp HOME
// (set via t.Setenv, so it's undone automatically) with the given
// default_server, so resolveServerAddr's fallback has something to read.
func writeDefaultServer(t *testing.T, defaultServer string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	path, err := credentials.DefaultPath()
