package server

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/guardrail"
	"github.com/footprintai/containarium/internal/guardrailpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func gpAdminCtx() context.Context {
	return auth.ContextWithTestSubject(context.Background(), "ops", auth.RoleAdmin)
}

func gpUserCtx() context.Context {
	return auth.ContextWithTestSubject(context.Background(), "data-owner", "user")
}

func gpSigner(seed byte) *pb.GuardrailTrustedSigner {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = seed
	}
	pub := ed25519.NewKeyFromSeed(s).Public().(ed25519.PublicKey)
	return &pb.GuardrailTrustedSigner{KeyId: guardrail.KeyID(pub), PublicKey: pub, Label: "release key"}
}

func gpRequest(maxResidual int32, signers ...*pb.GuardrailTrustedSigner) *pb.SetGuardrailPolicyRequest {
	return &pb.SetGuardrailPolicyRequest{
		Policy: &pb.GuardrailPolicy{Rules: []*pb.GuardrailRule{
			{Kind: pb.GuardrailKind_GUARDRAIL_KIND_PII, Action: pb.GuardrailAction_GUARDRAIL_ACTION_REDACT, MaxResidual: maxResidual},
			{Kind: pb.GuardrailKind_GUARDRAIL_KIND_SECRET, Action: pb.GuardrailAction_GUARDRAIL_ACTION_BLOCK},
		}},
		TrustedSigners: signers,
	}
}

func newGPServer() (*GuardrailPolicyServer, *fakeAuditLogger) {
	s := NewGuardrailPolicyServer(guardrailpolicy.NewMemoryStore())
	a := &fakeAuditLogger{}
	s.audit = a
	return s, a
}

// TestSetPolicy_AdminOnly: Set is admin-only (Q6); a non-admin is
// PERMISSION_DENIED, no subject is UNAUTHENTICATED, and neither changes the
// stored policy.
func TestSetPolicy_AdminOnly(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
		want codes.Code
	}{
		{"admin", gpAdminCtx(), codes.OK},
		{"non-admin", gpUserCtx(), codes.PermissionDenied},
		{"unauthenticated", context.Background(), codes.Unauthenticated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newGPServer()
			_, err := s.SetGuardrailPolicy(tc.ctx, gpRequest(0, gpSigner(1)))
			if got := status.Code(err); got != tc.want {
				t.Fatalf("SetGuardrailPolicy as %s = %v (%v), want %v", tc.name, got, err, tc.want)
			}
			get, err := s.GetGuardrailPolicy(gpAdminCtx(), &pb.GetGuardrailPolicyRequest{})
			if err != nil {
				t.Fatalf("GetGuardrailPolicy: %v", err)
			}
			if wantConfigured := tc.want == codes.OK; get.GetConfigured() != wantConfigured {
				t.Fatalf("configured after a %s Set = %v, want %v", tc.name, get.GetConfigured(), wantConfigured)
			}
		})
	}
}

// TestGetPolicy_AnyAuthenticatedCaller: a data owner (no admin role) can read
// the policy; an unauthenticated caller cannot; "never set" is configured=false,
// not an error.
func TestGetPolicy_AnyAuthenticatedCaller(t *testing.T) {
	s, _ := newGPServer()
	got, err := s.GetGuardrailPolicy(gpUserCtx(), &pb.GetGuardrailPolicyRequest{})
	if err != nil || got.GetConfigured() || got.GetPolicyHash() != "" {
		t.Fatalf("Get before any Set = (%v, %v), want configured=false and no hash", got, err)
	}
	if _, err := s.SetGuardrailPolicy(gpAdminCtx(), gpRequest(1, gpSigner(1))); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err = s.GetGuardrailPolicy(gpUserCtx(), &pb.GetGuardrailPolicyRequest{})
	if err != nil || !got.GetConfigured() {
		t.Fatalf("Get as a non-admin = (%v, %v), want the configured policy", got, err)
	}
	wantHash, _ := guardrail.PolicyHash(gpRequest(1).GetPolicy())
	if got.GetPolicyHash() != wantHash {
		t.Fatalf("policy_hash = %q, want the attestation hash of the rules %q", got.GetPolicyHash(), wantHash)
	}
	if !proto.Equal(got.GetPolicy().GetPolicy(), gpRequest(1).GetPolicy()) || got.GetPolicy().GetUpdatedBy() != "ops" {
		t.Fatalf("stored = %v, want the rules set by ops", got.GetPolicy())
	}
	if _, err := s.GetGuardrailPolicy(context.Background(), &pb.GetGuardrailPolicyRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("Get unauthenticated = %v, want Unauthenticated", err)
	}
}

type failingPolicyStore struct{ guardrailpolicy.Store }

func (failingPolicyStore) Get(context.Context) (*pb.ServerGuardrailPolicy, error) {
	return nil, errors.New("connection refused")
}

// TestGetPolicy_ReadErrorIsNotNotConfigured: a store read error is an RPC
// error, never configured=false, so no client can read an outage as "no
// policy".
func TestGetPolicy_ReadErrorIsNotNotConfigured(t *testing.T) {
	s := NewGuardrailPolicyServer(failingPolicyStore{guardrailpolicy.NewMemoryStore()})
	got, err := s.GetGuardrailPolicy(gpUserCtx(), &pb.GetGuardrailPolicyRequest{})
	if err == nil {
		t.Fatalf("Get with a failing store = %v, want an error", got)
	}
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal", status.Code(err))
	}
	// Get is open to non-admins: the database error stays in the daemon log.
	if msg := status.Convert(err).Message(); strings.Contains(msg, "connection refused") {
		t.Fatalf("Get error %q passes the store's error text to the caller", msg)
	}
}

// TestGuardrailPolicyStartupStore_FailsClosedWhenPostgresIsNotInstalled:
// with Postgres configured but its store not installed (pool unreachable or
// bootstrap failed, so dual_server passes nil), Get is a real error and never
// configured=false; the Provider read is an error distinct from
// ErrNotConfigured; and an admin Set is refused instead of landing in
// memory. With no Postgres configured at all, the in-memory store still
// serves, as before; with the Postgres store installed, it is the one used.
func TestGuardrailPolicyStartupStore_FailsClosedWhenPostgresIsNotInstalled(t *testing.T) {
	t.Run("postgres configured, store not installed", func(t *testing.T) {
		s := NewGuardrailPolicyServer(guardrailPolicyStartupStore(true, nil))
		got, err := s.GetGuardrailPolicy(gpUserCtx(), &pb.GetGuardrailPolicyRequest{})
		if status.Code(err) != codes.Internal {
			t.Fatalf("Get = %v, %v; want Internal, never configured=false", got, err)
		}
		if _, err := s.Provider().Get(context.Background()); err == nil || errors.Is(err, guardrailpolicy.ErrNotConfigured) {
			t.Fatalf("Provider().Get = %v, want an error distinct from ErrNotConfigured", err)
		}
		if _, err := s.SetGuardrailPolicy(gpAdminCtx(), gpRequest(0, gpSigner(1))); status.Code(err) != codes.Internal {
			t.Fatalf("admin Set = %v, want Internal (refused, not stored in memory)", err)
		}
		if _, err := s.GetGuardrailPolicy(gpUserCtx(), &pb.GetGuardrailPolicyRequest{}); status.Code(err) != codes.Internal {
			t.Fatalf("Get after the refused Set = %v, want Internal", err)
		}
	})
	t.Run("no postgres configured", func(t *testing.T) {
		s := NewGuardrailPolicyServer(guardrailPolicyStartupStore(false, nil))
		got, err := s.GetGuardrailPolicy(gpUserCtx(), &pb.GetGuardrailPolicyRequest{})
		if err != nil || got.GetConfigured() {
			t.Fatalf("Get = %v, %v; want configured=false, OK", got, err)
		}
		if _, err := s.SetGuardrailPolicy(gpAdminCtx(), gpRequest(0, gpSigner(1))); err != nil {
			t.Fatalf("admin Set = %v, want OK on the in-memory store", err)
		}
	})
	t.Run("dual_server wiring goes through the guard", func(t *testing.T) {
		// The behaviour above only holds if dual_server never hands the
		// server a store any other way. It must construct on the
		// fail-closed store and call SetStore exactly once, with
		// guardrailPolicyStartupStore(...).
		node, err := parser.ParseFile(token.NewFileSet(), "dual_server.go", nil, 0)
		if err != nil {
			t.Fatalf("parse dual_server.go: %v", err)
		}
		var setStores, guarded, failClosedCtor int
		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				if fn.Name == "NewGuardrailPolicyServer" && len(call.Args) == 1 {
					if lit, ok := call.Args[0].(*ast.CompositeLit); ok {
						if id, ok := lit.Type.(*ast.Ident); ok && id.Name == "unavailableGuardrailPolicyStore" {
							failClosedCtor++
