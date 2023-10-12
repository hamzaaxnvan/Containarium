// Package ttlsweeper enforces per-container TTLs by ticking once a
// minute and force-deleting any container whose ttl_expires_at has
// elapsed. The TTL itself is set by the SetContainerTTL RPC (see
// proto/containarium/v1/container.proto) and the CLI verb introduced
// in PR #297 (`containarium ttl set <box> <duration>`). The primary
// consumer is the containarium-run GitHub Action's "keep on failure"
// flow: failed-CI debug boxes get a 1-hour TTL stamped on them so they
// auto-clean instead of leaking.
