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
