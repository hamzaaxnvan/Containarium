package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// ErrNetworkPolicyNotFound is returned by a NetworkPolicyStore.Get when the
// tenant has no policy.
var ErrNetworkPolicyNotFound = errors.New("network policy not found")

// NetworkPolicyStore persists per-tenant NetworkPolicy values. Two impls:
// PostgresNetworkPolicyStore (durable, used when the daemon has a DB pool) and
// MemNetworkPolicyStore (in-memory, for --standalone daemons and tests).
type NetworkPolicyStore interface {
	Set(ctx context.Context, p *pb.NetworkPolicy) error
	Get(ctx context.Context, tenant string) (*pb.NetworkPolicy, error)
	List(ctx context.Context) ([]*pb.NetworkPolicy, error)
	Delete(ctx context.Context, tenant string) error
	// MutateDenyRules atomically reads the tenant's virtual-patch deny rules,
	// applies fn, and writes the result back — under a lock (mem) / row lock
	// (postgres) — so concurrent edits don't lose updates (#660). The allow-policy
	// is left untouched. If the tenant has no policy yet, a minimal one is created
	// (so an operator can virtual-patch before declaring an allow-policy). Returns
	// the resulting stored policy.
	MutateDenyRules(ctx context.Context, tenant string, fn func(existing []*pb.NetworkPolicyDenyRule) ([]*pb.NetworkPolicyDenyRule, error)) (*pb.NetworkPolicy, error)
}

// --- in-memory ------------------------------------------------------

// MemNetworkPolicyStore is a goroutine-safe in-memory store. Policies do not
// survive a daemon restart — used on --standalone daemons (no Postgres) and in
// tests.
type MemNetworkPolicyStore struct {
	mu sync.RWMutex
	m  map[string]*pb.NetworkPolicy
}

func NewMemNetworkPolicyStore() *MemNetworkPolicyStore {
	return &MemNetworkPolicyStore{m: make(map[string]*pb.NetworkPolicy)}
}

func (s *MemNetworkPolicyStore) Set(_ context.Context, p *pb.NetworkPolicy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	np := clonePolicy(p)
	// Set declares the allow-policy; virtual-patch deny rules are owned by
	// MutateDenyRules. Preserve the existing tenant's deny rules (a new tenant
	// starts with none) so `set` never clobbers them and needs no client read.
	if old, ok := s.m[p.GetTenant()]; ok {
		np.DenyRules = cloneDenyRules(old.GetDenyRules())
	} else {
		np.DenyRules = nil
	}
	s.m[p.GetTenant()] = np
	return nil
}

func (s *MemNetworkPolicyStore) MutateDenyRules(_ context.Context, tenant string, fn func([]*pb.NetworkPolicyDenyRule) ([]*pb.NetworkPolicyDenyRule, error)) (*pb.NetworkPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.m[tenant]
	if !ok {
		p = &pb.NetworkPolicy{Tenant: tenant}
	}
	newRules, err := fn(cloneDenyRules(p.GetDenyRules()))
	if err != nil {
		return nil, err
	}
	np := clonePolicy(p)
	np.DenyRules = newRules
	s.m[tenant] = np
	return clonePolicy(np), nil
}

func (s *MemNetworkPolicyStore) Get(_ context.Context, tenant string) (*pb.NetworkPolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.m[tenant]
	if !ok {
		return nil, ErrNetworkPolicyNotFound
	}
	return clonePolicy(p), nil
}

func (s *MemNetworkPolicyStore) List(_ context.Context) ([]*pb.NetworkPolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*pb.NetworkPolicy, 0, len(s.m))
	for _, p := range s.m {
		out = append(out, clonePolicy(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GetTenant() < out[j].GetTenant() })
	return out, nil
}

func (s *MemNetworkPolicyStore) Delete(_ context.Context, tenant string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, tenant)
	return nil
}

// clonePolicy makes a defensive copy so callers can't mutate stored state via
// the returned pointer (and vice versa).
func clonePolicy(p *pb.NetworkPolicy) *pb.NetworkPolicy {
	if p == nil {
		return nil
	}
	return &pb.NetworkPolicy{
		Tenant:           p.GetTenant(),
		AllowIntraTenant: p.GetAllowIntraTenant(),
		EgressCidrs:      append([]string(nil), p.GetEgressCidrs()...),
		EgressDomains:    append([]string(nil), p.GetEgressDomains()...),
		AllowMetadata:    p.GetAllowMetadata(),
		Mode:             p.GetMode(),
		Source:           p.GetSource(),
		DenyRules:        cloneDenyRules(p.GetDenyRules()),
		AllowFromTenants: append([]string(nil), p.GetAllowFromTenants()...),
		EgressPreset:     p.GetEgressPreset(),
		EgressMode:       p.GetEgressMode(),
	}
}

// cloneDenyRules deep-copies a deny-rule slice so stored state can't be mutated
// through a returned pointer (and vice versa).
func cloneDenyRules(in []*pb.NetworkPolicyDenyRule) []*pb.NetworkPolicyDenyRule {
	if in == nil {
		return nil
	}
	out := make([]*pb.NetworkPolicyDenyRule, len(in))
	for i, r := range in {
		out[i] = &pb.NetworkPolicyDenyRule{
			Cidr:      r.GetCidr(),
			Port:      r.GetPort(),
			Proto:     r.GetProto(),
			Note:      r.GetNote(),
			ExpiresAt: r.GetExpiresAt(),
		}
	}
	return out
}

// denyRuleRow is the JSON shape stored in the network_policies.deny_rules JSONB
// column. Local to the store so the persisted form is explicit and decoupled
// from the proto wire names.
type denyRuleRow struct {
	Cidr      string `json:"cidr"`
	Port      uint32 `json:"port,omitempty"`
	Proto     string `json:"proto,omitempty"`
	Note      string `json:"note,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

func encodeDenyRules(rules []*pb.NetworkPolicyDenyRule) ([]byte, error) {
	rows := make([]denyRuleRow, 0, len(rules))
	for _, r := range rules {
		rows = append(rows, denyRuleRow{r.GetCidr(), r.GetPort(), r.GetProto(), r.GetNote(), r.GetExpiresAt()})
	}
	return json.Marshal(rows)
}

func decodeDenyRules(b []byte) ([]*pb.NetworkPolicyDenyRule, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var rows []denyRuleRow
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, fmt.Errorf("decode deny_rules: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := make([]*pb.NetworkPolicyDenyRule, len(rows))
	for i, r := range rows {
		out[i] = &pb.NetworkPolicyDenyRule{Cidr: r.Cidr, Port: r.Port, Proto: r.Proto, Note: r.Note, ExpiresAt: r.ExpiresAt}
	}
	return out, nil
}

// --- postgres -------------------------------------------------------

