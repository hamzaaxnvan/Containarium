//go:build !containarium_client

package server

import (
	"context"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/modelgateway"
)

// gatewayOwnerRevocations is the daemon's choice of owner-revocation store
// (#2111). Both branches matter:
//
//   - With Postgres it must hand the gateway the durable store, or a restart
//     forgets every owner cutoff.
//   - Without Postgres the store pointer is nil, and a nil
//     *auth.PgOwnerRevocationStore placed straight into the interface field
//     would be a NON-nil interface holding a nil pointer: the gateway's
//     `OwnerRevocations == nil` guard would not fire and every keyed model call
//     would dereference nil. It must fall back to the in-memory store instead.
func TestGatewayOwnerRevocations_WiringChoice(t *testing.T) {
	durable := &auth.PgOwnerRevocationStore{}
