//go:build containarium_client

package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// osExit is os.Exit, indirected so tests can observe the exit code instead
// of killing the test binary.
var osExit = os.Exit

// movedServerCommands are every top-level server/operator command name that
// exists in containariumd but is tagged !containarium_client (#1774) out of
// the client build. Registering a stub for each — rather than leaving
// cobra's default "unknown command" — exists for exactly one failure mode
// (design doc, Rollout Phase 2): a host whose systemd unit or startup
// script still runs e.g. `containarium daemon` after the client build has
// taken over the `containarium` name. The stub's message names the fix.
//
// NOT "collaborator": #1785 made it the thirteenth hybrid command instead
// of a fully-moved one — collaboratorCmd (collaborator.go/_add/_list/
// _remove.go, no build tag) still registers under this exact name in the
// client build, with only its LOCAL-Incus code path stubbed out
// (local_stubs_client.go's addCollaboratorLocal/etc., all returning
// errNoLocalMode) rather than the whole command tree. "collaborator" had
// briefly been listed here too, registering a second, conflicting
// "collaborator" node alongside the real one; cobra has no defined
// tie-break for two same-named top-level commands, so which one
// rootCmd.Find("collaborator") returned was accidental and could flip
// with unrelated changes elsewhere in this package (found via
// TestMovedServerCommands_ExitTwoNamingContainariumd going from a
// coincidental pass to a deterministic fail while adding an unrelated new
// top-level command in #1921). Removed rather than kept in some disabled
// form — the hybrid implementation is the one with real dispatch logic
// and test coverage, so it's the one that should exist.
var movedServerCommands = []string{
	"daemon",
	"sentinel",
	"service",
	"tunnel",
	"node",
	"hypervisor-agent",
	"egress-relay",
	"upgrade-watchdog",
