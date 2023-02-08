package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeRevocationStore is a small in-memory stub of
// auth.RevocationStore for the RPC-level tests in this file.
// (The Postgres-backed impl is exercised by the integration
// suite.)
type fakeRevocationStore struct {
	mu            sync.Mutex
	revoked       map[string]string // jti -> reason
	revokedFamily map[string]string // family id -> reason
	failNext      bool
	failClaimNext bool
}

func newFakeRevocationStore() *fakeRevocationStore {
	return &fakeRevocationStore{revoked: map[string]string{}, revokedFamily: map[string]string{}}
}

func (f *fakeRevocationStore) IsRevoked(_ context.Context, jti string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.revoked[jti]
	return ok, nil
}

func (f *fakeRevocationStore) Revoke(_ context.Context, jti string, _ time.Time, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext {
		f.failNext = false
		return errors.New("simulated DB error")
	}
	if _, exists := f.revoked[jti]; !exists {
		f.revoked[jti] = reason
	}
	return nil
}

// RevokeClaim mirrors PgRevocationStore.RevokeClaim: it reports whether
// THIS call inserted the row, so tests can exercise the rotation path's
// claim-then-mint ordering the same way the real store would.
func (f *fakeRevocationStore) RevokeClaim(_ context.Context, jti string, _ time.Time, reason string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failClaimNext {
		f.failClaimNext = false
		return false, errors.New("simulated DB error")
	}
	if _, exists := f.revoked[jti]; exists {
		return false, nil
	}
	f.revoked[jti] = reason
	return true, nil
}

func (f *fakeRevocationStore) RevokeFamily(_ context.Context, familyID string, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.revokedFamily[familyID]; !exists {
		f.revokedFamily[familyID] = reason
	}
	return nil
}

func (f *fakeRevocationStore) IsFamilyRevoked(_ context.Context, familyID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.revokedFamily[familyID]
	return ok, nil
}

func (f *fakeRevocationStore) CleanupExpired(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}
func (f *fakeRevocationStore) List(_ context.Context, _ auth.ListRevocationsParams) ([]auth.Revocation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]auth.Revocation, 0, len(f.revoked))
	for jti, reason := range f.revoked {
		out = append(out, auth.Revocation{JTI: jti, Reason: reason})
	}
	return out, nil
}

func newTestTokensServer(t *testing.T, store auth.RevocationStore) *TokensServer {
	t.Helper()
	tm, err := auth.NewTokenManager("test-secret-must-be-at-least-32-bytes-long-ok", "test")
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	if store != nil {
		tm.SetRevocationStore(store)
	}
	return NewTokensServer(tm, store, time.Hour)
}

// --- authz gate ---

func TestRevokeToken_RejectsNonAdmin(t *testing.T) {
	srv := newTestTokensServer(t, newFakeRevocationStore())
	ctx := auth.ContextWithTestSubject(context.Background(), "alice", "user")
	_, err := srv.RevokeToken(ctx, &pb.RevokeTokenRequest{Jti: "x"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("non-admin: got %v want PermissionDenied", err)
	}
}

// Phase 1.7b — RevokeToken now requires the tokens:write
// scope in addition to the admin role. Admin without the
// scope must still be rejected (scopes are orthogonal).
func TestRevokeToken_RejectsAdminMissingScope(t *testing.T) {
	srv := newTestTokensServer(t, newFakeRevocationStore())
	ctx := auth.ContextWithTestSubjectScopes(context.Background(),
		"ops", []string{auth.RoleAdmin},
		[]string{auth.ScopeContainersRead}, // no tokens:write
	)
	_, err := srv.RevokeToken(ctx, &pb.RevokeTokenRequest{Jti: "x"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("admin without tokens:write: got %v want PermissionDenied", err)
	}
}

func TestRevokeToken_AdminWithScopePasses(t *testing.T) {
	store := newFakeRevocationStore()
	srv := newTestTokensServer(t, store)
	ctx := auth.ContextWithTestSubjectScopes(context.Background(),
		"ops", []string{auth.RoleAdmin},
		[]string{auth.ScopeTokensWrite},
	)
	resp, err := srv.RevokeToken(ctx, &pb.RevokeTokenRequest{Jti: "with-scope"})
	if err != nil {
		t.Fatalf("admin+tokens:write should pass; got %v", err)
	}
	if !resp.NewlyRevoked {
		t.Fatal("expected NewlyRevoked=true")
	}
}

func TestRevokeToken_RejectsNoSubject(t *testing.T) {
	srv := newTestTokensServer(t, newFakeRevocationStore())
	_, err := srv.RevokeToken(context.Background(), &pb.RevokeTokenRequest{Jti: "x"})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing subject: got %v want Unauthenticated", err)
	}
}

// --- validation ---

func TestRevokeToken_RejectsEmptyJTI(t *testing.T) {
	srv := newTestTokensServer(t, newFakeRevocationStore())
	ctx := auth.ContextWithTestSubject(context.Background(), "ops", auth.RoleAdmin)
	_, err := srv.RevokeToken(ctx, &pb.RevokeTokenRequest{Jti: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty jti: got %v want InvalidArgument", err)
	}
}

func TestRevokeToken_RejectsMalformedExpiry(t *testing.T) {
	srv := newTestTokensServer(t, newFakeRevocationStore())
	ctx := auth.ContextWithTestSubject(context.Background(), "ops", auth.RoleAdmin)
	_, err := srv.RevokeToken(ctx, &pb.RevokeTokenRequest{
		Jti:       "x",
		ExpiresAt: "not-a-date",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("malformed expires_at: got %v want InvalidArgument", err)
	}
}

func TestRevokeToken_UnavailableWhenStoreNil(t *testing.T) {
	srv := newTestTokensServer(t, nil)
	ctx := auth.ContextWithTestSubject(context.Background(), "ops", auth.RoleAdmin)
	_, err := srv.RevokeToken(ctx, &pb.RevokeTokenRequest{Jti: "x"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("nil store: got %v want Unavailable", err)
	}
}
