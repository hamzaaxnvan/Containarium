// Package dialer caches gRPC connections to agent-box's resident
// SpawnService, one per pool member's bind-mounted unix socket (#1488
// Phase 2/3). A claim reuses an already-established connection instead of
// paying dial cost on every spawn — the whole point of a warm pool is
// that the connection, like the container, is already up before the
// request arrives.
//
// This package deliberately does not reimplement reconnect/backoff logic:
// grpc-go's ClientConn already manages that transparently for a cached
// connection, and honors a call's context deadline regardless — an RPC
// against an unready channel returns once its own ctx expires rather than
// blocking forever, whether or not the call opts into WaitForReady. The
// tests in dialer_test.go exist to prove reconnect-after-disconnect and
// deadline-respecting empirically against a real local socket, not to
// assert this package's own internal state — grpc-go's behavior is
// exactly the contract this package depends on holding.
package dialer

import (
