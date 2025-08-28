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
