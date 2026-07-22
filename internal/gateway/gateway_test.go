package gateway

import (
	"testing"

	"github.com/footprintai/containarium/internal/auth"
)

// TestAuthorizeTenantWSAccess covers the tenant check shared by both
// WebSocket routes this gateway serves (/terminal and /console-attach).
// Replaces the old TestAuthRequiredForTerminal placeholder
// (security_test.go), which only logged what the code was supposed to do
// rather than asserting it — the gap that let #1754 (any authenticated
// tenant could open a shell into any other tenant's container via
// /terminal) go unnoticed.
func TestAuthorizeTenantWSAccess(t *testing.T) {
	cases := []struct {
		name      string
		claims    *auth.Claims
