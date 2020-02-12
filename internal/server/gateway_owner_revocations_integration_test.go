//go:build integration

// Postgres-backed owner revocation, end to end through the daemon's own
// wiring (#2111): removing a tenant's provider key revokes that owner's
// tokens, and the revocation survives a daemon restart.
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/server/
package server

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/modelgateway"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// The restart case the owner cutoff exists for: a token minted before the key
// was removed must not resolve through the daemon-global ProviderKeys fallback
// after the daemon comes back up.
func TestDeleteTenantProviderKey_PostgresBacked_RevokesAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	secret := []byte("model-gateway-test-secret-at-least-32-bytes")
	owner := modelgateway.UserKeyOwner("alice")

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"model":"m","usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	t.Cleanup(up.Close)

	// newDaemonGateway builds the gateway the way dual_server.go does, from a
	// store constructed on a fresh pool — each call is one daemon lifetime.
	// The daemon holds a GLOBAL key for the provider, which is exactly the
