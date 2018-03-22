package audit

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// #1605 — the native gRPC server produced no audit_logs row for any RPC;
// only the HTTP/grpc-gateway path had a writer. These tests cover
// buildEntry (the pure decision logic: what would this call's row look
// like, or should it not get one at all) without a real Postgres store,
// plus one end-to-end pass through GRPCInterceptor.Unary() with a fake
// logger standing in for *Store.

func testPeerContext(ctx context.Context, ip string, port int) context.Context {
	return peer.NewContext(ctx, &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP(ip), Port: port}})
}

func TestBuildEntry_PopulatesFields(t *testing.T) {
	md := metadata.Pairs(
		auth.MDKeyUsername, "alice",
		auth.MDKeyRoles, "admin,ops",
		RequestIDHeader, "trace-abc123",
	)
	ctx := metadata.NewIncomingContext(context.Background(), md)
	ctx = testPeerContext(ctx, "10.1.2.3", 5555)

	start := time.Now().Add(-50 * time.Millisecond)
	entry := buildEntry(ctx, "/containarium.v1.SecretsService/GetSecret", "grpc_unary", "trace-abc123", start, nil)
	if entry == nil {
		t.Fatal("buildEntry returned nil, want a populated entry")
	}
	if entry.Username != "alice" {
		t.Errorf("Username = %q, want alice", entry.Username)
	}
	if entry.ResourceType != "grpc" {
		t.Errorf("ResourceType = %q, want grpc", entry.ResourceType)
	}
	if entry.ResourceID != "/containarium.v1.SecretsService/GetSecret" {
		t.Errorf("ResourceID = %q, want the FullMethod", entry.ResourceID)
	}
	if entry.Action != "grpc_unary" {
		t.Errorf("Action = %q, want grpc_unary", entry.Action)
	}
	if entry.StatusCode != int(codes.OK) {
		t.Errorf("StatusCode = %d, want %d (OK)", entry.StatusCode, codes.OK)
	}
	if entry.SourceIP != "10.1.2.3:5555" {
		t.Errorf("SourceIP = %q, want peer addr", entry.SourceIP)
	}
	if !strings.Contains(entry.Detail, "request_id=trace-abc123") {
		t.Errorf("Detail = %q, missing request_id", entry.Detail)
	}
	if !strings.Contains(entry.Detail, "roles=admin,ops") {
		t.Errorf("Detail = %q, missing roles", entry.Detail)
