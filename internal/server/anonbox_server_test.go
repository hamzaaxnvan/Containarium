package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/anonbox"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/box"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

type fakeAnonEnsurer struct {
	got anonbox.EnsureRequest
	res *anonbox.EnsureResult
	err error
}

func (f *fakeAnonEnsurer) Ensure(_ context.Context, req anonbox.EnsureRequest) (*anonbox.EnsureResult, error) {
	f.got = req
	return f.res, f.err
}

type fakeAnonLister struct{ boxes []box.BoxStatus }

func (f *fakeAnonLister) List(context.Context) ([]box.BoxStatus, error) { return f.boxes, nil }

func anonSrv(ens *fakeAnonEnsurer, lst *fakeAnonLister) *AnonymousBoxServer {
	if ens == nil {
		ens = &fakeAnonEnsurer{res: &anonbox.EnsureResult{BoxName: "anon-deadbeef-container", SSHHost: "10.0.0.9", SSHPort: 22, SSHUser: "anon-deadbeef"}}
	}
	if lst == nil {
		lst = &fakeAnonLister{}
	}
	return NewAnonymousBoxServer(ens, lst, anonbox.DefaultLimits())
}

func anonScopedCtx(scopes ...string) context.Context {
	return auth.ContextWithTestSubjectScopes(context.Background(), "door", []string{"user"}, scopes)
}

// TestAnonymousBoxServer_Authz is the authz table from the design:
// Ensure = admin | anon:door; everything else = admin | anon:admin; a
// plain user with no scope is denied; no subject is Unauthenticated.
func TestAnonymousBoxServer_Authz(t *testing.T) {
	srv := anonSrv(nil, nil)
	ensure := func(ctx context.Context) error {
		_, err := srv.EnsureAnonymousBox(ctx, &pb.EnsureAnonymousBoxRequest{PublicKey: "ssh-ed25519 AAAA x"})
		return err
	}
	list := func(ctx context.Context) error {
		_, err := srv.ListAnonymousBoxes(ctx, &pb.ListAnonymousBoxesRequest{})
		return err
	}
	get := func(ctx context.Context) error {
		_, err := srv.GetAnonymousDoorConfig(ctx, &pb.GetAnonymousDoorConfigRequest{})
		return err
	}
	set := func(ctx context.Context) error {
		_, err := srv.SetAnonymousDoorConfig(ctx, &pb.SetAnonymousDoorConfigRequest{})
		return err
	}
	claim := func(ctx context.Context) error {
		_, err := srv.ClaimAnonymousBox(ctx, &pb.ClaimAnonymousBoxRequest{})
		return err
	}

	admin := auth.ContextWithTestSubject(context.Background(), "root", "admin")
	user := auth.ContextWithTestSubject(context.Background(), "alice", "user")
	none := context.Background()

	tests := []struct {
		name string
		call func(context.Context) error
		ctx  context.Context
		want codes.Code
	}{
		{"ensure/admin", ensure, admin, codes.OK},
		{"ensure/anon:door", ensure, anonScopedCtx(auth.ScopeAnonDoor), codes.OK},
		{"ensure/anon:admin only", ensure, anonScopedCtx(auth.ScopeAnonAdmin), codes.PermissionDenied},
		{"ensure/user", ensure, user, codes.PermissionDenied},
		{"ensure/no subject", ensure, none, codes.Unauthenticated},

		{"list/admin", list, admin, codes.OK},
		{"list/anon:admin", list, anonScopedCtx(auth.ScopeAnonAdmin), codes.OK},
		{"list/anon:door only", list, anonScopedCtx(auth.ScopeAnonDoor), codes.PermissionDenied},
		{"list/user", list, user, codes.PermissionDenied},

		{"get/anon:admin", get, anonScopedCtx(auth.ScopeAnonAdmin), codes.OK},
		{"get/user", get, user, codes.PermissionDenied},

		// Stubs: gated first, Unimplemented only for an authorized caller.
		{"set/admin", set, admin, codes.Unimplemented},
		{"set/user", set, user, codes.PermissionDenied},
		{"claim/anon:admin", claim, anonScopedCtx(auth.ScopeAnonAdmin), codes.Unimplemented},
		{"claim/anon:door", claim, anonScopedCtx(auth.ScopeAnonDoor), codes.PermissionDenied},
		{"claim/no subject", claim, none, codes.Unauthenticated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := status.Code(tt.call(tt.ctx)); got != tt.want {
				t.Errorf("code = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnsureAnonymousBox_MapsRequestAndResponse(t *testing.T) {
	exp := time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC)
	ens := &fakeAnonEnsurer{res: &anonbox.EnsureResult{
		BoxName: "anon-1a2b3c4d-container", SSHHost: "10.100.0.57", SSHPort: 22, SSHUser: "anon-1a2b3c4d",
		TTLExpiresAt: exp, Reused: true, PreviousExpired: false,
	}}
	srv := anonSrv(ens, nil)

