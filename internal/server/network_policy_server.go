package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/netpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// errDenyRuleNotFound aborts a pure-removal patch (no adds) that matched no
// existing deny rule, so PatchNetworkPolicyDenyRules can report NotFound and the
// transaction rolls back rather than committing a no-op.
var errDenyRuleNotFound = errors.New("deny rule not found")

// NetworkPolicyServer implements the control-plane CRUD for per-tenant network
// policies (Phase A, #315). It validates/normalizes via internal/netpolicy and
// persists through a NetworkPolicyStore. Admin-only — policies are
// fleet-shaping config, not tenant-editable. The per-veth TC_INGRESS BPF loader
// (a later increment) consumes the stored policies.
type NetworkPolicyServer struct {
	pb.UnimplementedNetworkPolicyServiceServer
	tenantGuardRead      func() *pb.GuardStatus
	tenantGuardReconcile func(context.Context) error
	tenantStoreDurable   func() bool
	store                NetworkPolicyStore
	sigStore             NetworkPolicySignatureStore // #661 PR-B: operator exploit signatures (global)

	// degraded is true while store is the in-memory fallback standing in for
	// a configured Postgres that was unreachable at startup (#3131). Stored
	// inverted so the zero value means durable. Read on every call, so
	// InstallDurableStore ends the degraded state with no restart.
	degraded atomic.Bool

	// auditQuery and gateway serve PlanNetworkPolicy (#2440). Both are
	// optional: without an audit store the RPC is Unavailable, and without a
	// gateway the plan simply does not subtract the preset's implicit resolver.
	auditQuery denyAuditQuerier
	gateway    netip.Addr

	// applied and enforceArmed serve GetNetworkPolicyStatus: the enforcer's
	// loaded state (nil = no enforcer on this daemon) and the host's
	// CONTAINARIUM_NETWORK_POLICY_ENFORCE.
	applied      appliedStateSource
	enforceArmed bool
	// kernelUnsupported says whether this host's kernel is known to be too old
	// for the egress program (#2972); nil asks the host. Tests inject it.
	kernelUnsupported func() bool
	clock             func() time.Time // nil = time.Now; tests inject a fixed clock
}

// denyAuditQuerier is the slice of *audit.Store PlanNetworkPolicy needs.
type denyAuditQuerier interface {
	Query(ctx context.Context, params audit.QueryParams) ([]audit.AuditEntry, int32, error)
}

// SetPlanSources wires what PlanNetworkPolicy reads: the audit store holding
// the enforcer's deny rows and the bridge gateway (the preset's implicit
// resolver). Startup-only. Either may be zero/nil.
func (s *NetworkPolicyServer) SetPlanSources(q denyAuditQuerier, gateway netip.Addr) {
	s.auditQuery = q
	s.gateway = gateway
}

func NewNetworkPolicyServer(store NetworkPolicyStore) *NetworkPolicyServer {
	return &NetworkPolicyServer{store: store}
}

// SetSignatureStore wires the operator-signature store (#661 PR-B). Startup-only,
// same contract as SetStore. Nil leaves the signature RPCs returning Unavailable.
func (s *NetworkPolicyServer) SetSignatureStore(store NetworkPolicySignatureStore) {
	s.sigStore = store
}

// SignatureStore returns the operator-signature store (for the enforcer to read
// during reconcile). Nil until SetSignatureStore is called.
func (s *NetworkPolicyServer) SignatureStore() NetworkPolicySignatureStore {
	return s.sigStore
}

// SetStore swaps the backing store. Intended for startup only — called during
// NewDualServer (before the gRPC server starts serving) to upgrade the initial
// in-memory store to the Postgres-backed one once the DB connection string is
// resolved. Not safe to call concurrently with live RPCs.
func (s *NetworkPolicyServer) SetStore(store NetworkPolicyStore) {
	s.store = store
}

// SetDurable records whether the current store is the source of truth: true
// with no Postgres configured (the in-memory store IS the store) or once the
// Postgres store is installed; false while Postgres was configured but could
// not be reached at startup (#3131).
func (s *NetworkPolicyServer) SetDurable(durable bool) { s.degraded.Store(!durable) }

// Durable reports whether the current store is the source of truth. The
// operator mutation RPCs and the tenant guard's policy source read it on every
// call.
func (s *NetworkPolicyServer) Durable() bool { return !s.degraded.Load() }

// InstallDurableStore makes store (the Postgres one) the source of truth and
// ends the degraded state, so the operator mutation RPCs accept writes again
// without a restart. Same concurrency contract as SetStore for the swap itself.
func (s *NetworkPolicyServer) InstallDurableStore(store NetworkPolicyStore) {
	s.SetStore(store)
	s.SetDurable(true)
}

// errNetworkPolicyStoreDegraded is what an operator mutation gets while the
// store is the in-memory fallback: the write would be lost on restart, so it
// is refused before anything is touched (#3131).
var errNetworkPolicyStoreDegraded = status.Error(codes.Unavailable,
	"network-policy store is degraded: running on the in-memory fallback (Postgres unavailable at startup); nothing was changed")

// requireDurable gates the operator mutation RPCs. Reads are not gated, and
// neither are the daemon's in-process writers (cloud policy sync, agent-skill
// launch policy, auto-quarantine), which write to Store() directly: refusing
// them would drop a live enforcement effect for the degraded window.
func (s *NetworkPolicyServer) requireDurable() error {
	if s.degraded.Load() {
		return errNetworkPolicyStoreDegraded
	}
	return nil
}

// Store returns the backing store. Used by the network-policy enforcer to read
// stored policies for reconcile; call after any startup-time SetStore swap.
func (s *NetworkPolicyServer) Store() NetworkPolicyStore {
	return s.store
}

func (s *NetworkPolicyServer) SetNetworkPolicy(ctx context.Context, req *pb.SetNetworkPolicyRequest) (*pb.SetNetworkPolicyResponse, error) {
	if err := auth.RequireRole(ctx, auth.RoleAdmin); err != nil {
		return nil, err
	}
	if err := s.requireDurable(); err != nil {
		return nil, err
	}
	if err := auth.RequireScope(ctx, auth.ScopeNetworkPolicyWrite); err != nil {
		return nil, err
	}
	// Validate + normalize (parse/dedup CIDRs, normalize domains, resolve mode).
	compiled, err := netpolicy.Compile(req.GetPolicy())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	stored := compiled.ToProto()
	if err := s.store.Set(ctx, stored); err != nil {
		return nil, status.Errorf(codes.Internal, "store network policy: %v", err)
	}
	return &pb.SetNetworkPolicyResponse{Policy: stored}, nil
}

func (s *NetworkPolicyServer) GetNetworkPolicy(ctx context.Context, req *pb.GetNetworkPolicyRequest) (*pb.GetNetworkPolicyResponse, error) {
	if err := auth.RequireRoleOrScope(ctx, auth.RoleAdmin, auth.ScopeNetworkPolicyRead); err != nil {
		return nil, err
	}
	if req.GetTenant() == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant is required")
	}
	p, err := s.store.Get(ctx, req.GetTenant())
	if err != nil {
		if errors.Is(err, ErrNetworkPolicyNotFound) {
			return nil, status.Errorf(codes.NotFound, "no network policy for tenant %q", req.GetTenant())
		}
		return nil, status.Errorf(codes.Internal, "get network policy: %v", err)
	}
	return &pb.GetNetworkPolicyResponse{Policy: p, Revision: PolicyRevision(p)}, nil
}

func (s *NetworkPolicyServer) ListNetworkPolicies(ctx context.Context, _ *pb.ListNetworkPoliciesRequest) (*pb.ListNetworkPoliciesResponse, error) {
	if err := auth.RequireRoleOrScope(ctx, auth.RoleAdmin, auth.ScopeNetworkPolicyRead); err != nil {
		return nil, err
