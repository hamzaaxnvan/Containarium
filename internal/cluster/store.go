//go:build !containarium_client

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the persistence contract for managed clusters. Two
// implementations exist — MemStore (daemon-without-Postgres degrade
// path + unit tests) and PGStore — held to the same behavior by the
// integration test suite (house rule from #1300: the contract is
// stated once, against both).
type Store interface {
	Create(ctx context.Context, c *Cluster) error
	Get(ctx context.Context, owner, name string) (*Cluster, error)
	// List returns clusters for one owner, or every cluster when
	// owner is empty, ordered by (owner, name).
	List(ctx context.Context, owner string) ([]*Cluster, error)
	SetState(ctx context.Context, owner, name string, st State, reason string) error
	SetEndpoint(ctx context.Context, owner, name, endpoint string) error
	UpdateNodeGroups(ctx context.Context, owner, name string, groups []NodeGroup) error
	// SetNodeGroupTarget updates exactly one group's TargetNodes,
	// atomically with respect to a concurrent call for a DIFFERENT
	// group on the same cluster (#1882). UpdateNodeGroups' full-array
	// replace is the wrong tool for this: the CA-provider handler for
	// one group and the handler for a sibling group can each read a
	// stale copy of the whole NodeGroups array before either writes,
	// and whichever writes last overwrites the other's already-
	// committed target with its own stale copy of it — observed live
	// as the reconciler recreating a node the autoscaler had, only
	// seconds earlier, correctly scaled to zero and deleted. Returns
	// ErrNotFound if the cluster doesn't exist; a group name absent
	// from the cluster's groups is a silent no-op (nothing to update),
	// matching UpdateNodeGroups' own leniency.
	SetNodeGroupTarget(ctx context.Context, owner, name, group string, target int32) error
	// Delete removes the cluster and (transitively) its nodes and
	// events — the "re-created cluster starts empty" guarantee.
	Delete(ctx context.Context, owner, name string) error
	UpsertNode(ctx context.Context, n *Node) error
	ListNodes(ctx context.Context, owner, name string) ([]*Node, error)
	// DeleteNode is scoped to (owner, cluster): a VM name is only
	// deletable through its own cluster, so a caller holding a
	// constructible cross-tenant VM name cannot remove another
	// tenant's node row.
	DeleteNode(ctx context.Context, owner, name, vmName string) error
	AppendEvent(ctx context.Context, owner, name string, e Event) error
	// ListEvents returns the newest events first, at most limit
	// (limit <= 0 = all).
	ListEvents(ctx context.Context, owner, name string, limit int) ([]Event, error)
	Close()
}

// PGStore is the Postgres-backed Store.
type PGStore struct {
	pool *pgxpool.Pool
}

// NewPGStore initializes the schema on a shared pool (house pattern:
// idempotent DDL in the constructor; the pool is owned by the caller).
func NewPGStore(ctx context.Context, pool *pgxpool.Pool) (*PGStore, error) {
	s := &PGStore{pool: pool}
	if err := s.initSchema(ctx); err != nil {
		return nil, fmt.Errorf("init k8s cluster schema: %w", err)
	}
	return s, nil
}

func (s *PGStore) initSchema(ctx context.Context) error {
	schema := `
		CREATE TABLE IF NOT EXISTS k8s_clusters (
			id UUID PRIMARY KEY,
			owner TEXT NOT NULL,
			name TEXT NOT NULL,
			state TEXT NOT NULL,
			state_reason TEXT NOT NULL DEFAULT '',
			k3s_version TEXT NOT NULL DEFAULT '',
			api_endpoint TEXT NOT NULL DEFAULT '',
			node_groups JSONB NOT NULL,
			node_isolation TEXT NOT NULL DEFAULT 'vm',
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL,
			UNIQUE(owner, name)
		);
		CREATE INDEX IF NOT EXISTS idx_k8s_clusters_owner ON k8s_clusters(owner);
		-- #1428: clusters recorded before the isolation class existed
		-- were all VMs; the DEFAULT states that for the existing rows
		-- rather than leaving their boundary unknown.
		ALTER TABLE k8s_clusters ADD COLUMN IF NOT EXISTS node_isolation TEXT NOT NULL DEFAULT 'vm';

		CREATE TABLE IF NOT EXISTS k8s_cluster_nodes (
			vm_name TEXT PRIMARY KEY,
			cluster_id UUID NOT NULL REFERENCES k8s_clusters(id) ON DELETE CASCADE,
			role TEXT NOT NULL,
			node_group TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_k8s_cluster_nodes_cluster ON k8s_cluster_nodes(cluster_id);

		CREATE TABLE IF NOT EXISTS k8s_cluster_events (
			id BIGSERIAL PRIMARY KEY,
			cluster_id UUID NOT NULL REFERENCES k8s_clusters(id) ON DELETE CASCADE,
			at TIMESTAMPTZ NOT NULL,
			kind TEXT NOT NULL,
			node_group TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_k8s_cluster_events_cluster ON k8s_cluster_events(cluster_id, id DESC);
	`
