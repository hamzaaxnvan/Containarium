package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync/atomic"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/codeegress"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// codeEgressClusterDefaultResourceID names the cluster default in audit
// entries, where an empty resource id would read as "unknown".
const codeEgressClusterDefaultResourceID = "_cluster_default"

// CodingToolEgressPolicyServer serves the admin-approved egress allowlist for
// the coding tool (#2378, docs/architecture/coding-cli-egress-allowlist.md).
// Set and Delete are admin-only; Get is for an admin, or the tenant itself
// holding code-egress:read. Store only: nothing here programs the kernel.
type CodingToolEgressPolicyServer struct {
	pb.UnimplementedCodingToolEgressPolicyServiceServer
	// store is swapped while serving when the recovery retry installs the
	// Postgres store (#2483).
	store    *swappableCodeEgressStore
	audit    auditLogger
	implicit codeegress.Implicit
	// durable says whether store is the source of truth: true with no
	// Postgres configured (the in-memory store IS the store) or once the
	// Postgres store is installed; false while Postgres is configured but
	// its store is not in place. Then the in-memory store is an empty
	// stand-in, and answering from it would report every tenant as
	// unrestricted and drop admin writes at restart, so every RPC fails
	// with UNAVAILABLE instead (the network-policy store's
	// policyStoreDurable guard, #2359).
	durable atomic.Bool
}

func NewCodingToolEgressPolicyServer(store CodingToolEgressPolicyStore) *CodingToolEgressPolicyServer {
	s := &CodingToolEgressPolicyServer{store: &swappableCodeEgressStore{}}
	s.store.swap(store)
	s.durable.Store(true)
	return s
}

// swappableCodeEgressStore is a CodingToolEgressPolicyStore whose backing
// store can be replaced while RPCs are in flight (#2483).
type swappableCodeEgressStore struct {
	cur atomic.Pointer[codeEgressStoreRef]
}

type codeEgressStoreRef struct{ CodingToolEgressPolicyStore }

func (w *swappableCodeEgressStore) swap(st CodingToolEgressPolicyStore) {
	w.cur.Store(&codeEgressStoreRef{st})
}

func (w *swappableCodeEgressStore) current() CodingToolEgressPolicyStore {
	return w.cur.Load().CodingToolEgressPolicyStore
}

func (w *swappableCodeEgressStore) Set(ctx context.Context, p *pb.CodingToolEgressPolicy, updatedBy string) (*pb.CodingToolEgressPolicy, int64, error) {
	return w.current().Set(ctx, p, updatedBy)
}

func (w *swappableCodeEgressStore) Get(ctx context.Context, tenant string) (*pb.CodingToolEgressPolicy, error) {
	return w.current().Get(ctx, tenant)
}

func (w *swappableCodeEgressStore) Delete(ctx context.Context, tenant string) (int64, error) {
	return w.current().Delete(ctx, tenant)
}

// SetStore swaps the backing store. Safe while serving; it does not change
// the durable flag (InstallDurableStore does both).
func (s *CodingToolEgressPolicyServer) SetStore(store CodingToolEgressPolicyStore) {
	s.store.swap(store)
}
