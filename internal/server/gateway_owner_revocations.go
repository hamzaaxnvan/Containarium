//go:build !containarium_client

package server

import (
	"log"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/modelgateway"
)

// The durable store satisfies the gateway's read half at compile time. Its
// write half (RevokeOwner, which RevokeByKeyOwner type-asserts for) is an
// unexported interface in modelgateway, so it is pinned by the tests instead:
// DeleteTenantProviderKey must report tokens_revoked=true on a
// Postgres-backed daemon.
var _ modelgateway.OwnerRevocationChecker = (*auth.PgOwnerRevocationStore)(nil)

// gatewayOwnerRevocations picks the model gateway's owner-revocation store
// (the "customer removed their key" kill-switch, #1725/#2111).
//
// With Postgres, the durable store: an owner cutoff survives a daemon restart,
// so a token minted before its owner's key was removed cannot resolve through
// the daemon-global key once the daemon comes back.
//
