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
// mounts it as a second MCP server beside agent-box. The shape is pinned by
// agent-runtime/fixtures/platform_mcp.json, which both this package's tests and
// the runtime's seed.test.ts read.
type platformMCPSeed struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	// ServerURL is the daemon REST base URL as reachable from inside the box.
	ServerURL string `json:"server_url"`
	// TokenFile is the run's seeded platform JWT. The MCP server re-reads it on
	// every request (CONTAINARIUM_JWT_TOKEN_FILE), so the seed never carries the
	// token itself.
	TokenFile string `json:"token_file"`
	// Tools is the CONTAINARIUM_MCP_TOOLS allow-list — "tracker_*", so the agent
	// sees the tracker tools and not the other ~75 platform tools. The JWT's own
	// scopes still gate every call; this narrows what is offered.
	Tools string `json:"tools"`
}

// platformMCPSeedScript returns the shell that writes <seedDir>/platform_mcp.json,
// or ok=false when this run should not get one: a run not bound to a tracker
// connection has nothing to reach through the tracker tools, and without the
// daemon's HTTP port the URL would be unusable.
