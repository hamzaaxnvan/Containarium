package secrets

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/modelgateway"
	corecrypto "github.com/footprintai/containarium/pkg/core/secrets"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The store is the model gateway's per-owner key source; the interface it has
// to satisfy is declared over there (narrow, local — modelgateway keeps no
// dependency on this package, exactly like RevocationChecker).
var _ modelgateway.KeyResolver = (*Store)(nil)

// TestGatewayKeyLocation pins where a per-owner provider key lives: the
// reserved path __gateway/<key_owner>/<provider>, split into the store's
// (username, name) pair. A '/' can never appear in a tenant username, so the
// reserved namespace is disjoint from every tenant's by construction rather
// than by a filter someone has to remember.
func TestGatewayKeyLocation(t *testing.T) {
	tests := []struct {
		name         string
		keyOwner     string
		provider     string
		wantUsername string
		wantSecret   string
		wantPath     string
		wantErr      bool
	}{
		{
			name: "org owner", keyOwner: "org:3f2b1c4d-0000-4000-8000-000000000001", provider: "acmeai",
			wantUsername: "__gateway/org:3f2b1c4d-0000-4000-8000-000000000001",
			wantSecret:   "ACMEAI",
			wantPath:     "__gateway/org:3f2b1c4d-0000-4000-8000-000000000001/acmeai",
		},
		{
			name: "user owner", keyOwner: "user:bob", provider: "gemini-openai",
			wantUsername: "__gateway/user:bob",
			wantSecret:   "GEMINI_OPENAI",
			wantPath:     "__gateway/user:bob/gemini-openai",
		},
		{name: "empty owner", keyOwner: "", provider: "acmeai", wantErr: true},
		{name: "owner with a slash", keyOwner: "org:a/b", provider: "acmeai", wantErr: true},
		{name: "owner with whitespace", keyOwner: "org:a b", provider: "acmeai", wantErr: true},
		{name: "owner with a newline", keyOwner: "org:a\nb", provider: "acmeai", wantErr: true},
		{name: "empty provider", keyOwner: "user:bob", provider: "", wantErr: true},
		{name: "provider with bad characters", keyOwner: "user:bob", provider: "acme/ai", wantErr: true},
		{name: "upper-case provider", keyOwner: "user:bob", provider: "ACMEAI", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			username, secretName, err := GatewayKeyLocation(tc.keyOwner, tc.provider)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("GatewayKeyLocation(%q, %q) = (%q, %q), want an error", tc.keyOwner, tc.provider, username, secretName)
				}
				return
			}
			if err != nil {
				t.Fatalf("GatewayKeyLocation(%q, %q): %v", tc.keyOwner, tc.provider, err)
			}
			if username != tc.wantUsername || secretName != tc.wantSecret {
				t.Errorf("= (%q, %q), want (%q, %q)", username, secretName, tc.wantUsername, tc.wantSecret)
			}
			// The rendered path is the one the design names, so a log line or
			// an audit row reads the same as the doc.
			if got := username + "/" + strings.ToLower(strings.ReplaceAll(secretName, "_", "-")); got != tc.wantPath {
				t.Errorf("rendered path = %q, want %q", got, tc.wantPath)
			}
			if !IsGatewayKeyNamespace(username) {
				t.Errorf("IsGatewayKeyNamespace(%q) = false", username)
			}
			// The secret name must be storable — the store's own name rule is
			// env-var-shaped, which is why the owner/provider pair cannot be
			// one literal name.
			if err := corecrypto.ValidateName(secretName); err != nil {
				t.Errorf("secret name %q is not storable: %v", secretName, err)
			}
		})
	}

