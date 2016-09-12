package server

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Fail-closed guard for the coding-tool egress policy store (#2378): with
// Postgres configured but its store not in place, the in-memory store is an
// empty stand-in. Answering from it would report every tenant as
// unrestricted and lose admin writes at restart.

// startupServer is the server as NewDualServer leaves it when Postgres is
// configured: the in-memory stand-in, marked not durable.
func startupServer(t *testing.T) *CodingToolEgressPolicyServer {
	t.Helper()
	s, _ := newTestCodeEgressServer()
	s.SetDurable(false)
	return s
}

func assertCodeEgressUnavailable(t *testing.T, s *CodingToolEgressPolicyServer) {
	t.Helper()
	ctx := ceAdminCtx()
	enforce := pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE
	if _, err := s.SetCodingToolEgressPolicy(ctx, setReq("alice", enforce, []string{"192.0.2.0/24"}, nil)); codeOf(err) != codes.Unavailable {
		t.Errorf("Set = %v, want UNAVAILABLE", err)
	}
	for _, tenant := range []string{"alice", ""} {
		resp, err := s.GetCodingToolEgressPolicy(ctx, &pb.GetCodingToolEgressPolicyRequest{Tenant: tenant})
		if codeOf(err) != codes.Unavailable {
			t.Errorf("Get(%q) = %v (restricted=%v), want UNAVAILABLE, not an answer from the empty stand-in",
				tenant, err, resp.GetEffective().GetRestricted())
		}
	}
	if _, err := s.DeleteCodingToolEgressPolicy(ctx, &pb.DeleteCodingToolEgressPolicyRequest{Tenant: "alice"}); codeOf(err) != codes.Unavailable {
		t.Errorf("Delete = %v, want UNAVAILABLE", err)
	}
	// Nothing reached the stand-in.
	if _, err := s.store.Get(context.Background(), "alice"); !errors.Is(err, ErrCodingToolEgressPolicyNotFound) {
		t.Errorf("stand-in store was written: %v", err)
	}
}

func TestCodeEgressPolicy_FailsClosedWhenDurableStoreMissing(t *testing.T) {
	assertCodeEgressUnavailable(t, startupServer(t))
}

func TestCodeEgressPolicy_FailsClosedWhenPostgresStoreInitFails(t *testing.T) {
	s := startupServer(t)
	err := s.InstallDurableStore(func() (CodingToolEgressPolicyStore, error) {
		return nil, errors.New("create table: connection refused")
	})
	if err == nil {
		t.Fatal("InstallDurableStore: want the opener's error")
	}
	assertCodeEgressUnavailable(t, s)
}

func TestCodeEgressPolicy_AuthStillCheckedBeforeDurability(t *testing.T) {
	s := startupServer(t)
	_, err := s.SetCodingToolEgressPolicy(ceTenantCtx("alice"),
		setReq("alice", pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE, []string{"192.0.2.0/24"}, nil))
	if codeOf(err) != codes.PermissionDenied {
		t.Errorf("non-admin Set = %v, want PERMISSION_DENIED", err)
	}
}

func TestCodeEgressPolicy_ServesOnceDurableStoreInstalled(t *testing.T) {
	s := startupServer(t)
	if err := s.InstallDurableStore(func() (CodingToolEgressPolicyStore, error) {
		return NewMemCodingToolEgressPolicyStore(), nil
	}); err != nil {
		t.Fatalf("InstallDurableStore: %v", err)
	}
	ctx := ceAdminCtx()
	if _, err := s.SetCodingToolEgressPolicy(ctx, setReq("alice", pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE, []string{"192.0.2.0/24"}, nil)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	resp, err := s.GetCodingToolEgressPolicy(ctx, &pb.GetCodingToolEgressPolicyRequest{Tenant: "alice"})
	if err != nil || !resp.GetEffective().GetRestricted() {
		t.Fatalf("Get = %v, restricted=%v; want the stored policy", err, resp.GetEffective().GetRestricted())
	}
}

// With no Postgres configured the in-memory store IS the store: unchanged.
func TestCodeEgressPolicy_NoPostgresServesFromMemory(t *testing.T) {
	s, _ := newTestCodeEgressServer()
	s.SetDurable(true) // NewDualServer: postgresConnString == ""
	resp, err := s.GetCodingToolEgressPolicy(ceAdminCtx(), &pb.GetCodingToolEgressPolicyRequest{Tenant: "alice"})
	if err != nil || resp.GetEffective().GetRestricted() {
		t.Fatalf("Get = %v, restricted=%v; want unrestricted from the in-memory store", err, resp.GetEffective().GetRestricted())
	}
}

// NewDualServer needs a database and Incus, so the startup wiring is checked
// over the parsed AST (as in dual_server_wiring_test.go):
//   - codeEgressServer.SetDurable(postgresConnString == "") runs before the
//     store is installed;
//   - the store goes in through InstallDurableStore (SetStore alone would
//     leave it marked not durable, or bypass the guard);
//   - that call is not nested inside the network-policy store's branch, so a
//     failure there does not leave this store on the stand-in.
func TestNewDualServer_CodeEgressStoreFailsClosedAndIsWiredIndependently(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "dual_server.go", nil, 0)
	if err != nil {
		t.Fatalf("parse dual_server.go: %v", err)
	}
	var fn *ast.FuncDecl
	for _, d := range file.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == "NewDualServer" {
			fn = f
		}
	}
	if fn == nil {
		t.Fatal("NewDualServer not found in dual_server.go")
	}

	src := func(n ast.Node) string {
		var b strings.Builder
		_ = printer.Fprint(&b, fset, n)
		return b.String()
	}
	isCodeEgressCall := func(n ast.Node, method string) (*ast.CallExpr, bool) {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return nil, false
