//go:build containarium_client

package cmd

import (
	"strings"
	"testing"
)

// TestMovedServerCommands_ExitTwoNamingContainariumd is #1777's Done-when:
// each moved-command name is registered in the client build as a leaf stub
// (not the real command — those are tagged !containarium_client and don't
// exist in this build at all) that exits 2.
func TestMovedServerCommands_ExitTwoNamingContainariumd(t *testing.T) {
