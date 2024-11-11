//go:build !containarium_client

package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/footprintai/containarium/internal/guardrailpolicy"
	guardrailpolicypg "github.com/footprintai/containarium/internal/guardrailpolicy/pgstore"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Retry cadence for the durable policy-store install (#2483): every 5 s,
// doubling to a 60 s cap.
const (
	policyStoreRetryInitial = 5 * time.Second
	policyStoreRetryMax     = 60 * time.Second
)

// errInMemoryPolicyStore is what the recovery loop returns when an opener
// hands it an in-memory store. Installing one would answer "no policy" /
// "unrestricted" from an empty store and lose admin writes at restart, which
// is exactly what the fail-closed stand-in exists to prevent.
var errInMemoryPolicyStore = errors.New("refusing to install an in-memory store as the durable policy store")

// policyStoreRecovery installs the Postgres guardrail and coding-tool egress
// policy stores after startup when Postgres was configured but could not be
// reached (#2483). Until a store is installed it stays on its fail-closed
// stand-in and its health status is NOT_SERVING; on install the store is
// swapped in atomically and the status turns SERVING.
type policyStoreRecovery struct {
	guardrail *GuardrailPolicyServer
	egress    *CodingToolEgressPolicyServer
	health    *health.Server

	// connect opens one pool, one attempt (no internal retry).
	connect       func(ctx context.Context) (*pgxpool.Pool, error)
	openGuardrail func(ctx context.Context, pool *pgxpool.Pool) (guardrailpolicy.Store, error)
	openEgress    func(ctx context.Context, pool *pgxpool.Pool) (CodingToolEgressPolicyStore, error)

	initial, max time.Duration
	after        func(time.Duration) <-chan time.Time
	logf         func(format string, args ...any)
}

func newPolicyStoreRecovery(gp *GuardrailPolicyServer, ce *CodingToolEgressPolicyServer, h *health.Server, connString string) *policyStoreRecovery {
	return &policyStoreRecovery{
		guardrail: gp,
		egress:    ce,
		health:    h,
		connect: func(context.Context) (*pgxpool.Pool, error) {
			return connectToPostgres(connString, 1, 0)
		},
		openGuardrail: func(ctx context.Context, pool *pgxpool.Pool) (guardrailpolicy.Store, error) {
			return guardrailpolicypg.New(ctx, pool)
		},
		openEgress: func(ctx context.Context, pool *pgxpool.Pool) (CodingToolEgressPolicyStore, error) {
			return NewPostgresCodingToolEgressPolicyStore(ctx, pool)
		},
		initial: policyStoreRetryInitial,
		max:     policyStoreRetryMax,
		after:   time.After,
		logf:    log.Printf,
	}
}

// syncHealth publishes each store's readiness on the health service.
func (r *policyStoreRecovery) syncHealth() {
	set := func(svc string, ready bool) {
		st := healthpb.HealthCheckResponse_NOT_SERVING
		if ready {
			st = healthpb.HealthCheckResponse_SERVING
		}
		r.health.SetServingStatus(svc, st)
	}
