// Package ttlsweeper enforces per-container TTLs by ticking once a
// minute and force-deleting any container whose ttl_expires_at has
// elapsed. The TTL itself is set by the SetContainerTTL RPC (see
// proto/containarium/v1/container.proto) and the CLI verb introduced
// in PR #297 (`containarium ttl set <box> <duration>`). The primary
// consumer is the containarium-run GitHub Action's "keep on failure"
// flow: failed-CI debug boxes get a 1-hour TTL stamped on them so they
// auto-clean instead of leaking.
//
// # Design split (why this package has no daemon wiring)
//
// This package intentionally ships in two halves:
//
//  1. THIS scaffold — pure decision logic (Decide) plus a Manager with
//     ticker + interface seams (IncusClient, Deleter). Zero dependency
//     on the heavy Incus client; the ContainerView struct carries
//     exactly the fields the sweeper reads.
//
//  2. A follow-up wiring PR — adapts incus.ContainerInfo →
//     ContainerView at the call site, implements the SetContainerTTL
//     server handler (deciding whether to persist into Incus user.*
//     config keys, SQLite, or both), constructs a Manager and starts
//     it from the daemon's main loop, provides a real Deleter that
//     invokes the existing DeleteContainer path with the right
//     audit-event banner.
