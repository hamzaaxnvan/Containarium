//go:build !windows && !containarium_client

package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/sentinel"
	"github.com/spf13/cobra"
)

const sentinelDeregisterTokenTestSecret = "cli-deregister-admin-secret-32-bytes!!"

// resetSentinelDeregisterTokenFlags clears the package-level flag vars
// between tests, so one test's settings cannot leak into another's.
func resetSentinelDeregisterTokenFlags() {
	sentinelDeregisterTokenSentinelURL = ""
	sentinelDeregisterTokenToken = ""
	sentinelDeregisterTokenTokenPrefix = ""
	sentinelDeregisterTokenSecret = ""
}

func newSentinelDeregisterTokenTestCmd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetContext(context.Background())
	return cmd
}

// TestSentinelDeregisterTokenCmd_RequiresTokenOrPrefix pins the CLI-first
// requirement (#1963): the command must reject a call that supplies
// neither identifier, mirroring the handler's own 400.
func TestSentinelDeregisterTokenCmd_RequiresTokenOrPrefix(t *testing.T) {
	t.Cleanup(resetSentinelDeregisterTokenFlags)
	resetSentinelDeregisterTokenFlags()
	sentinelDeregisterTokenSentinelURL = "http://unused.invalid"
	sentinelDeregisterTokenSecret = sentinelDeregisterTokenTestSecret

	err := runSentinelDeregisterToken(newSentinelDeregisterTokenTestCmd(), nil)
	if err == nil {
		t.Fatal("want an error when neither --token nor --token-prefix is set")
	}
	if !strings.Contains(err.Error(), "--token") {
		t.Errorf("error = %q, want it to mention --token/--token-prefix", err)
	}
}

// TestSentinelDeregisterTokenCmd_RejectsBothTokenAndPrefix mirrors the
// handler's ambiguous-request 400.
func TestSentinelDeregisterTokenCmd_RejectsBothTokenAndPrefix(t *testing.T) {
	t.Cleanup(resetSentinelDeregisterTokenFlags)
	resetSentinelDeregisterTokenFlags()
	sentinelDeregisterTokenSentinelURL = "http://unused.invalid"
	sentinelDeregisterTokenSecret = sentinelDeregisterTokenTestSecret
	sentinelDeregisterTokenToken = "host-a.secret1"
	sentinelDeregisterTokenTokenPrefix = "host-a."

	err := runSentinelDeregisterToken(newSentinelDeregisterTokenTestCmd(), nil)
