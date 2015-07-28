package incus

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Issue #1483: on a byoc node, the daemon's core-container boot wait
// (waitForCoreContainers in internal/cmd/daemon.go) can get stuck at
// "Waiting for core containers to be ready..." forever, reporting `active`
// while never progressing and never triggering systemd's Restart=on-failure.
//
// waitForCoreContainers already re-resolves each core container's address
// on every poll (it does not latch a stale IP) and already has an outer
// 2-minute context.WithTimeout wrapping the loop. But that outer bound is
// only checked at the TOP of each loop iteration (`select { case
// <-ctx.Done(): ...; default: }`) — it does nothing to bound a single call
// *inside* one iteration. incusClient.FindContainerByRole ultimately calls
// c.server.GetInstance / c.server.GetInstanceNames on the incus SDK's
// InstanceServer, obtained via incus.ConnectIncusUnix(socketPath, nil).
// Passing nil ConnectionArgs leaves HTTPClient unset, so the SDK's HTTP
// transport has no http.Client-level deadline (only a 3600s
// ResponseHeaderTimeout baked into the transport itself, which is ~30x
// longer than the daemon's own 2-minute outer bound) — a slow-to-answer
// incusd (e.g. busy mounting a LUKS2/TPM2-unlocked pool right after boot)
// can park the goroutine inside that single HTTP round-trip well past the
// outer bound, and the ctx.Done() check never gets a chance to re-run
// because the goroutine isn't spinning through the loop — it's blocked
// inside one call.
//
// These tests exercise that mechanism directly against a fake Incus unix
// socket that accepts connections but never answers them, standing in for
// an incusd that is alive (the socket exists, connect succeeds) but too
// busy to respond to any API request yet.

// listenSilentIncusSocket starts a unix socket listener that accepts every
// connection and then does nothing further with it — no read, no write, no
// close — simulating an incusd that has bound its socket but is still busy
// (e.g. mounting storage) and has not yet gotten around to servicing any
// HTTP request on it. Accepted connections are tracked and force-closed on
// test cleanup so any client goroutine still blocked on one unblocks
// (with a connection error) instead of leaking past the test.
func listenSilentIncusSocket(t *testing.T) string {
	t.Helper()

	// Unix socket paths have a short max length (~104 bytes on
	// Linux/macOS); t.TempDir() embeds the full test name and can blow
	// past that for a long subtest name, so use a short-prefixed temp
	// dir instead (same fix as internal/hypervisor/provider_test.go's
	// shortTempDir).
	dir, err := os.MkdirTemp("", "incus-to")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sockPath := filepath.Join(dir, "incus.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("failed to listen on fake incus socket: %v", err)
	}

	done := make(chan struct{})
	connCh := make(chan net.Conn, 64)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				close(done)
				return
