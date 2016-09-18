package audit

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// GatewayForwardMDKey marks a gRPC call as one grpc-gateway made in-process
// while translating a REST request (internal/gateway/gateway.go's
// annotateContext sets it on every outgoing call). GRPCInterceptor checks
// incoming metadata for it and skips writing a row when present — the HTTP
// audit middleware already logged the same request at the REST layer, and
// without this guard every REST call would produce two audit_logs rows
// (#1605 acceptance criterion 4: "make sure that produces one row, not two").
const GatewayForwardMDKey = "x-containarium-gateway-forward"

// auditLogger is the write surface GRPCInterceptor needs from a *Store —
// narrowed to one method so tests can substitute a fake without a real
// Postgres connection. Production callers pass a *Store via SetStore.
type auditLogger interface {
	Log(ctx context.Context, entry *AuditEntry) error
}

// GRPCInterceptor audits the native gRPC server the way HTTPAuditMiddleware
// audits the REST/grpc-gateway path (#1605). Before this, audit_logs was fed
// by exactly one writer — the HTTP middleware — so an RPC served on the
// native gRPC port produced no row while the identical RPC served over REST
// produced one; which of the two an operator's action landed in depended on
// the client they happened to use, not on what the action was.
//
// The audit store isn't available until well after the gRPC server is
// constructed (dual_server.go's NewDualServer connects to Postgres later in
// the same function), so this holds a settable reference rather than a
// fixed one — the same shape as GatewayServer.SetAuditStore /
// ContainerServer.SetAuditStore. Register Unary()/Stream() with
// grpc.NewServer() immediately; they no-op until SetStore is called.
type GRPCInterceptor struct {
	mu      sync.RWMutex
	store   auditLogger
	entryCh chan *AuditEntry
}

// NewGRPCInterceptor returns an interceptor with no store attached yet and
// starts its background writer goroutine (same async-write shape as
// HTTPAuditMiddleware, so a slow Postgres write never adds latency to the
// RPC it's auditing). Safe to register with grpc.NewServer() right away.
func NewGRPCInterceptor() *GRPCInterceptor {
	g := &GRPCInterceptor{entryCh: make(chan *AuditEntry, 256)}
	go func() {
		for entry := range g.entryCh {
			st := g.getStore()
			if st == nil {
				continue
			}
			if err := st.Log(context.Background(), entry); err != nil {
				log.Printf("audit: failed to write grpc log: %v", err)
				recordPersistFailure()
			}
		}
