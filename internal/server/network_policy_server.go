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

