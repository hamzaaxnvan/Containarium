package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// The protect/unprotect verbs set a container's delete policy (#284) via the
// SetContainerDeletePolicy RPC (POST /v1/containers/{name}/delete-policy),
// which stamps user.containarium.delete_policy on the Incus container — the
// exact key the daemon's ttlsweeper auto-reap and `containarium prune` consult
// to SKIP a box. Protecting a box guards it against an unattended "clean up
// leaked boxes" sweep taking out, say, a persistent CI runner; it does NOT
// block a deliberate `containarium delete` (that always succeeds).
//
// The current policy is read back via `containarium get` / `list` (the
// delete_policy field on the container view), so there is no separate "get"
// verb here — mirrors how `ttl get` reads ttl_expires_at off the container.
//
// A daemon too old to implement SetContainerDeletePolicy surfaces
// codes.Unimplemented (gRPC) / HTTP 404 mapped to Unimplemented (REST); the CLI
// degrades to a friendly no-op via isUnimplemented (shared with ttl.go) rather
