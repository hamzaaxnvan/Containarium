package server

import (
	"encoding/json"
	"fmt"
	"strings"
)

// platformMCPHostPlaceholder stands in for the daemon host inside the seed
// JSON until the in-box script substitutes the real one. The host is the box's
// default-route gateway, which only the box can resolve (it depends on the
// bridge subnet) — the same in-box resolution gatewayEnvScript uses.
const platformMCPHostPlaceholder = "__CTN_HOST__"

// platformMCPAllowInsecureEnv is the MCP client's plaintext opt-in (audit
// C-HIGH-1, internal/mcp/client_tls.go). Only that process's environment ever
// carries it, via the `env` wrapper in platformMCPSeedScript.
const platformMCPAllowInsecureEnv = "CONTAINARIUM_MCP_ALLOW_INSECURE"

// platformMCPSeed is the Go side of the daemon <-> in-box runtime contract for
// mounting the platform MCP (design decision D4, docs/architecture/
// agent-tracker-broker.md). Written as <seed>/platform_mcp.json; the runtime
