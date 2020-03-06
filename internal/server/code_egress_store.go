package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// ErrCodingToolEgressPolicyNotFound is returned by Get when no policy is
// stored under the key.
var ErrCodingToolEgressPolicyNotFound = errors.New("coding-tool egress policy not found")

// CodingToolEgressPolicyStore persists coding-tool egress policies (#2378),
// keyed by tenant; the empty tenant is the cluster default. Separate from
// NetworkPolicyStore (its own table) so a tenant's NetworkPolicy never
// changes meaning. Callers pass a policy already validated by
// codeegress.Normalize.
type CodingToolEgressPolicyStore interface {
	// Set replaces the policy under p.Tenant, assigning the next revision
	// (monotonic across all keys and deletes) and stamping updated_at and
	// updatedBy. It returns the stored policy and the revision it replaced
	// (0 when there was none).
	Set(ctx context.Context, p *pb.CodingToolEgressPolicy, updatedBy string) (stored *pb.CodingToolEgressPolicy, previousRevision int64, err error)
	Get(ctx context.Context, tenant string) (*pb.CodingToolEgressPolicy, error)
	// Delete removes the policy and returns the revision it removed (0 when
	// there was none; deleting a missing policy is not an error).
	Delete(ctx context.Context, tenant string) (removedRevision int64, err error)
}

// --- in-memory ------------------------------------------------------

// MemCodingToolEgressPolicyStore is goroutine-safe and does not survive a
// restart; for --standalone daemons and tests.
type MemCodingToolEgressPolicyStore struct {
	mu       sync.RWMutex
	m        map[string]*pb.CodingToolEgressPolicy
	revision int64
}

func NewMemCodingToolEgressPolicyStore() *MemCodingToolEgressPolicyStore {
	return &MemCodingToolEgressPolicyStore{m: make(map[string]*pb.CodingToolEgressPolicy)}
}

func (s *MemCodingToolEgressPolicyStore) Set(_ context.Context, p *pb.CodingToolEgressPolicy, updatedBy string) (*pb.CodingToolEgressPolicy, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var prev int64
	if old, ok := s.m[p.GetTenant()]; ok {
		prev = old.GetRevision()
	}
	s.revision++
	np := cloneCodeEgressPolicy(p)
	np.Revision = s.revision
	np.UpdatedAt = timestamppb.New(time.Now().UTC())
	np.UpdatedBy = updatedBy
	s.m[p.GetTenant()] = np
	return cloneCodeEgressPolicy(np), prev, nil
}

func (s *MemCodingToolEgressPolicyStore) Get(_ context.Context, tenant string) (*pb.CodingToolEgressPolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.m[tenant]
	if !ok {
		return nil, ErrCodingToolEgressPolicyNotFound
	}
	return cloneCodeEgressPolicy(p), nil
}

func (s *MemCodingToolEgressPolicyStore) Delete(_ context.Context, tenant string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
