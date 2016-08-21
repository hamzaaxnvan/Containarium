package server

import (
	"context"
	"log"

	"github.com/footprintai/containarium/internal/ttlsweeper"
	"github.com/footprintai/containarium/pkg/core/incus"
)

// ttlsweeperSandboxAdapter bridges incus.Backend -> ttlsweeper.IncusClient,
// scoped to ephemeral sandboxes only (#1488 Phase 4). Sibling of
// ttlsweeperIncusAdapter/ttlsweeperBoxAdapter (ttlsweeper_adapter.go,
// ttlsweeper_boxes_adapter.go): same one-method seam, filtered to
// instances carrying SandboxKindLabelKey rather than "every non-core
// container".
//
// Only the absolute TTL rule ever applies to a sandbox — Stopped,
// StoppedAt, DeleteAfterStopped, and Protected are all left zero, so
// Decide's stopped->delete and protected-box rules never fire: a sandbox
// is never stopped-and-kept (DeleteSandbox always destroys outright, see
// its doc comment) and delete-protection is a persistent-tenant-box
