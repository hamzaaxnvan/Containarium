// Package expose holds the business logic for "make a container port
// reachable on a public hostname." Both the `containarium expose-port`
// CLI subcommand and the platform MCP's `expose_port` tool delegate to
// this package — the CLI is the canonical surface, and the MCP tool is
// a thin wrapper around the same Run() function via a transport-specific
// adapter implementing APIClient.
//
// Adding a new transport (e.g. CLI HTTP mode) is one adapter type with
// two methods. The validation, IP-resolution, and orchestration stay
// here so behavior can never drift between CLI and MCP.
package expose

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// exposeReadyTimeout / exposePollInterval bound how long Run waits for a
// freshly-created box to get its IP before failing. An agent commonly creates
// a box and immediately exposes a port, racing the box's bring-up — it has no
// IP until it's RUNNING. Waiting absorbs that race instead of erroring (the
// same create→ready fix connect got). vars (not consts) so tests can shrink.
var (
	exposeReadyTimeout = 40 * time.Second
	exposePollInterval = 3 * time.Second
)

// APIClient is the minimal transport surface this package needs. The
// CLI implements it against client.GRPCClient; the MCP tool implements
// it against mcp.Client (HTTP). Tests use a fake.
type APIClient interface {
	// LookupContainer finds a container by its identifier (the same
	// "username" used by create_container / get_container) and returns
	// the canonical container name plus its current LAN IP. State is
	// returned for error messages when the IP isn't yet assigned.
	LookupContainer(ctx context.Context, username string) (name, ip, state string, err error)
