//go:build !windows

package cmd

import (
	"strings"
	"testing"
)

// #2759: the CLI exposes the auto-sleep opt-out and says what it is for.
func TestNoAutoSleepFlag(t *testing.T) {
	f := createCmd.Flags().Lookup("no-auto-sleep")
