//go:build containarium_client

package cmd

import (
	"errors"

	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// errNoLocalMode is what every stub below returns. The containarium client
// has no local-Incus code path at all (see
// docs/architecture/cli-client-server-split.md) — this is the message a
// user sees if they somehow reach one of these stubs (in practice,
