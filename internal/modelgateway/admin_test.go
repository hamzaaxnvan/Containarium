package modelgateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestRevokeRequest_JSONShape is the golden shared by the server's decoder
// (admin.go) and cmd/model-gateway's `revoke --jti` subcommand, which builds
// this same struct to construct the request body. If either side's field
// names, order, or omitempty behavior drifts, this is the test that catches
// it — a wire-shape mismatch here would otherwise only surface as a live 400
// between two binaries built from the same commit.
func TestRevokeRequest_JSONShape(t *testing.T) {
	full := RevokeRequest{
		JTI:       "abc123",
		ExpiresAt: "2026-01-01T00:00:00Z",
		Reason:    "leaked",
	}
	const goldenFull = `{"jti":"abc123","expires_at":"2026-01-01T00:00:00Z","reason":"leaked"}`

	got, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != goldenFull {
		t.Errorf("Marshal(%+v) = %s, want %s", full, got, goldenFull)
	}

	var decoded RevokeRequest
	if err := json.Unmarshal([]byte(goldenFull), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded != full {
		t.Errorf("Unmarshal(%s) = %+v, want %+v", goldenFull, decoded, full)
	}

	// Reason is optional — the CLI's revoke subcommand may be called without
	// one — and must not appear at all when empty (omitempty), not as "".
	noReason := RevokeRequest{JTI: "abc123", ExpiresAt: "2026-01-01T00:00:00Z"}
	const goldenNoReason = `{"jti":"abc123","expires_at":"2026-01-01T00:00:00Z"}`
	got2, err := json.Marshal(noReason)
	if err != nil {
		t.Fatalf("Marshal (no reason): %v", err)
	}
	if string(got2) != goldenNoReason {
		t.Errorf("Marshal(%+v) = %s, want %s", noReason, got2, goldenNoReason)
	}
}

// TestAdminRevoke_Table exercises POST /__gateway/revoke end to end through
// Gateway.Handler(): the route's very existence (AdminToken empty vs set),
// the bearer check, request validation, and the success path against a real
// MemRevocations.
func TestAdminRevoke_Table(t *testing.T) {
	const adminToken = "s3cr3t-admin-token"

	validBody := func() string {
		b, err := json.Marshal(RevokeRequest{
			JTI:       "jti-1",
			ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339),
