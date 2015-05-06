package mcp

import (
	"log"
	"os"
	"strconv"

	"github.com/footprintai/containarium/internal/config"
	"github.com/footprintai/containarium/internal/credentials"
)

// Config holds configuration for the MCP server
type Config struct {
	// ServerURL is the base URL of the Containarium REST API
	// Example: http://localhost:8080 or https://containarium.example.com
	ServerURL string

	// JWTToken is the JWT token for authentication. Either this or
	// JWTTokenFile must be set. JWTToken is captured once at MCP
	// server start; it can't reflect a rotation without a restart.
	// For long-running MCP processes that need to survive token
	// rotation, prefer JWTTokenFile.
	JWTToken string

	// JWTTokenFile, when set, points to a file containing the JWT
	// token. The file is re-read on every request to the Containarium
	// API, so rotating the token is a single `mv newtoken oldpath`
	// step — no MCP restart needed. Empty means use JWTToken instead.
	// Whitespace around the token in the file is trimmed.
	JWTTokenFile string

	// Debug enables debug logging
	Debug bool

	// MCPTools is the operator-facing tool allow-list (#1922 step 7):
	// a comma-separated list of exact tool names and/or "prefix_*"
	// globs. Empty (the default) means every registered tool is
	// available — today's behavior, unchanged. Set to curate what an
	// in-box agent sees when this MCP server is mounted with a
	// narrowly-scoped JWT (e.g. "tracker_*" for the tracker broker's
	// tools only) — on top of, not instead of, the existing per-tool
	// RequiredScope check. See filterToolsByAllowlist.
	MCPTools string
}

// LoadConfig loads configuration from environment variables, with a
// final fallback to ~/.containarium/credentials.json (the file
// `containarium login` writes — see internal/credentials). The
// precedence is, highest first:
//
//  1. CONTAINARIUM_JWT_TOKEN (static, in-process)
//  2. CONTAINARIUM_JWT_TOKEN_FILE (re-read per request — rotation-safe)
//  3. credentials.json lookup for ServerURL (or DefaultServer when
//     ServerURL is empty). Static for the life of the process —
//     prefer (2) when you need long-running token rotation.
//
// Fallback (3) is best-effort: any error reading credentials.json is
