package server

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/footprintai/containarium/internal/netpolicy"
	"github.com/footprintai/containarium/internal/safecast"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// ErrSignatureNotFound is returned by a signature store's delete/get for an
// unknown name (delete treats it as a no-op; the server maps it to idempotent
// success).
var ErrSignatureNotFound = errors.New("network-policy signature not found")

// NetworkPolicySignatureStore persists operator-managed Tier 2 exploit
// signatures (#661 PR-B). They are GLOBAL (not tenant-scoped). Set upserts by
// name and assigns a stable id (>= netpolicy.OperatorIDBase) the first time a
// name is seen, so the id echoed in a match's audit row never changes under an
// operator.
type NetworkPolicySignatureStore interface {
	Set(ctx context.Context, name string, pattern []byte, enabled bool, note string) (*pb.NetworkPolicySignature, error)
	List(ctx context.Context) ([]*pb.NetworkPolicySignature, error)
	Delete(ctx context.Context, name string) error
}

// --- in-memory ------------------------------------------------------

type memSignature struct {
	id      uint16
	pattern []byte
	enabled bool
	note    string
}

// MemNetworkPolicySignatureStore is a goroutine-safe in-memory store used on
// --standalone daemons and in tests.
type MemNetworkPolicySignatureStore struct {
	mu     sync.RWMutex
	m      map[string]memSignature
	nextID uint16
}

func NewMemNetworkPolicySignatureStore() *MemNetworkPolicySignatureStore {
	return &MemNetworkPolicySignatureStore{m: make(map[string]memSignature), nextID: netpolicy.OperatorIDBase}
}

func (s *MemNetworkPolicySignatureStore) Set(_ context.Context, name string, pattern []byte, enabled bool, note string) (*pb.NetworkPolicySignature, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.m[name]
	id := cur.id
	if !ok {
		id = s.nextID
		s.nextID++
	}
	rec := memSignature{id: id, pattern: append([]byte(nil), pattern...), enabled: enabled, note: note}
	s.m[name] = rec
	return sigToProto(name, rec), nil
}

func (s *MemNetworkPolicySignatureStore) List(_ context.Context) ([]*pb.NetworkPolicySignature, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*pb.NetworkPolicySignature, 0, len(s.m))
	for name, rec := range s.m {
		out = append(out, sigToProto(name, rec))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GetName() < out[j].GetName() })
	return out, nil
}

func (s *MemNetworkPolicySignatureStore) Delete(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[name]; !ok {
		return ErrSignatureNotFound
	}
	delete(s.m, name)
	return nil
}

func sigToProto(name string, rec memSignature) *pb.NetworkPolicySignature {
	return &pb.NetworkPolicySignature{
		Name:    name,
		Pattern: string(rec.pattern),
		Enabled: rec.enabled,
		Id:      uint32(rec.id),
