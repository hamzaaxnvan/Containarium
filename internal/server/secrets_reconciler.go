package server

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/secrets"
	"github.com/footprintai/containarium/pkg/core/incus"
)

// Phase 4.3 Phase B-3 — file-mode secret reconciler
// (audit C-MED-4 polish).
//
// File-mode secrets live in `/run/secrets/<NAME>` inside
// the container. `/run` is tmpfs on every systemd distro,
// so the directory evaporates whenever the container
// stops. The daemon's canonical start path
// (StartContainer / CreateContainer / RefreshSecrets) re-
// stamps on every transition, so operators using
// `containarium start <user>` are covered.
//
// But: a bare `incus restart` not routed through the
// daemon doesn't trigger the daemon's stamp logic. The
// container comes up with an empty `/run/secrets`, the
// app tries to open its credentials, ENOENT, crash. That's
// the failure this reconciler closes.
//
// Strategy: a ticker that, every interval, asks the
// secrets store for the set of tenants with at least one
// file-mode secret, looks up each tenant's container in
// Incus, and (if Running) re-stamps. The stamp is
// idempotent — writing the same content over the same
// path is fine — so periodic re-stamps cost nothing when
// state is already correct.
//
// Skipped on every tick:
//   - Tenants with no file-mode secrets (env-mode rows
//     don't need this — incus config survives restart).
//   - Containers that are Stopped (the stamp would race
//     with a future start and is wasted work; the start
//     path will re-stamp when it runs).
//
// The reconciler is intentionally simple. It doesn't try
// to deduplicate or track per-container state — the
// idempotency of the stamp is the whole correctness
// argument. If a future profiling pass shows the
// reconciler doing real work, the per-container last-
// observed-running timestamp would be the obvious
