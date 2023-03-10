//go:build integration

package server

import (
	"context"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	guardrailpolicypg "github.com/footprintai/containarium/internal/guardrailpolicy/pgstore"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #2483 against a real database: the daemon starts with Postgres down, both
// policy APIs fail closed, Postgres comes up, and on the next retry the
// Postgres stores are installed: the RPCs succeed and what they store is in
// the database, not in memory.
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/server/ -run PolicyStoreRecovery

// downDSN points at a port nothing listens on, so connecting is a real
// "connection refused".
func downDSN(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return "postgres://containarium:test@" + addr + "/containarium?sslmode=disable&connect_timeout=2"
}
