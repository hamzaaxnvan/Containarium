//go:build containarium_client

package cmd

import (
	"errors"
	"os"
	"testing"
)

// TestHybridCommands_NoServer_NeverReachRealLocalMode is #1775's Done-when
// table-driven test: with no --server (and no CONTAINARIUM_SERVER/
// credentials.json fallback in play), every one of the thirteen hybrid
// commands (#1785 added collaborator) must error via a *Local stub — never
// via a working local-Incus call, because under containarium_client every
// *Local symbol IS one of
// those stubs (local_stubs_client.go). errors.Is against errNoLocalMode is
// exactly the assertion that proves this: it can only be true if the
// call reached a stub, since errNoLocalMode is not constructible any other
// way in this build.
func TestHybridCommands_NoServer_NeverReachRealLocalMode(t *testing.T) {
	// isCloudTarget (used by `info`) reads $HOME/.containarium/credentials.json
	// to resolve default_server's cached access model. Redirect HOME to an
	// empty temp dir so this test is hermetic — it must not depend on (or be
	// broken by) whoever is actually logged in on the machine running it.
	t.Setenv("HOME", t.TempDir())

	tests := []struct {
		name  string
		setup func()
