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
	if err == nil {
		t.Fatal("want an error when both --token and --token-prefix are set")
	}
}

// TestSentinelDeregisterTokenCmd_RequiresSecret mirrors register-token's own
// guard against calling out with no admin secret at all.
func TestSentinelDeregisterTokenCmd_RequiresSecret(t *testing.T) {
	t.Cleanup(resetSentinelDeregisterTokenFlags)
	resetSentinelDeregisterTokenFlags()
	sentinelDeregisterTokenSentinelURL = "http://unused.invalid"
	sentinelDeregisterTokenToken = "host-a.secret1"

	err := runSentinelDeregisterToken(newSentinelDeregisterTokenTestCmd(), nil)
	if err == nil {
		t.Fatal("want an error when no admin secret is configured")
	}
}

// TestSentinelDeregisterTokenCmd_EndToEndExactToken drives the real
// operator path for the unchanged exact-token form: the cobra command signs
// a DELETE, the real sentinel handler serves it behind the real HMAC
// middleware, and the token stops validating.
func TestSentinelDeregisterTokenCmd_EndToEndExactToken(t *testing.T) {
	var mgr sentinel.Manager
	mgr.SetAdminSecret([]byte(sentinelDeregisterTokenTestSecret))
	policy := sentinel.NewTokenPolicy()
	policy.Allow("live-token", sentinel.PoolAny)
	mgr.SetTunnelPolicy(policy)
	mgr.SetTunnelTokenStorePath(t.TempDir() + "/tunnel-tokens.json")

	mux := newSentinelTunnelTokenTestMux(&mgr)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	t.Cleanup(resetSentinelDeregisterTokenFlags)
	resetSentinelDeregisterTokenFlags()
	sentinelDeregisterTokenSentinelURL = srv.URL
	sentinelDeregisterTokenToken = "live-token"
	sentinelDeregisterTokenSecret = sentinelDeregisterTokenTestSecret

	if err := runSentinelDeregisterToken(newSentinelDeregisterTokenTestCmd(), nil); err != nil {
		t.Fatalf("runSentinelDeregisterToken: %v", err)
	}
	if err := policy.Validate("live-token", ""); err == nil {
		t.Fatal("token still valid after CLI deregistration")
	}
}

// TestSentinelDeregisterTokenCmd_EndToEndTokenPrefix is the CLI-first
// coverage for #1963's actual point: decommissioning a host by id, removing
// both its original join token and a reissued reconnect token, without the
// caller ever supplying either plaintext token.
func TestSentinelDeregisterTokenCmd_EndToEndTokenPrefix(t *testing.T) {
	var mgr sentinel.Manager
	mgr.SetAdminSecret([]byte(sentinelDeregisterTokenTestSecret))
	policy := sentinel.NewTokenPolicy()
	policy.Allow("host-a.secret1", sentinel.PoolAny)
	policy.Allow("host-a.secret2", sentinel.PoolAny)
	policy.Allow("host-b.secret1", sentinel.PoolAny)
	mgr.SetTunnelPolicy(policy)
	mgr.SetTunnelTokenStorePath(t.TempDir() + "/tunnel-tokens.json")

	mux := newSentinelTunnelTokenTestMux(&mgr)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
