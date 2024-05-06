//go:build !windows && !containarium_client

package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/sentinel"
	"github.com/spf13/cobra"
)

// #2415 — `sentinel register-token|deregister-token --kind audit-ingest`.

const kindTestSecret = "cli-kind-admin-secret-32-bytes-long!!"

func resetKindFlags() {
	sentinelRegisterTokenSentinelURL, sentinelRegisterTokenToken = "", ""
	sentinelRegisterTokenPools, sentinelRegisterTokenSecret = nil, ""
	sentinelRegisterTokenKind, sentinelRegisterTokenBackend = sentinelTokenKindTunnelJoin, ""
	sentinelDeregisterTokenSentinelURL, sentinelDeregisterTokenToken = "", ""
	sentinelDeregisterTokenTokenPrefix, sentinelDeregisterTokenSecret = "", ""
	sentinelDeregisterTokenKind, sentinelDeregisterTokenBackend = sentinelTokenKindTunnelJoin, ""
}

func kindTestCmd() *cobra.Command {
	c := &cobra.Command{}
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	c.SetContext(context.Background())
	return c
}

type captured struct {
	method, path string
	body         []byte
}

// capturingSentinel is an HMAC-gated stub that records the request it got.
func capturingSentinel(t *testing.T, got *captured) *httptest.Server {
	t.Helper()
	h := auth.SentinelHMACMiddleware([]byte(kindTestSecret), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path = r.Method, r.URL.Path
		got.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func TestSentinelTokenKind_Set(t *testing.T) {
	var k sentinelTokenKind
	for _, ok := range []string{"tunnel-join", "audit-ingest"} {
		if err := k.Set(ok); err != nil || string(k) != ok {
