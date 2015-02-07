package main

import (
	"log"
	"os"

	"github.com/footprintai/containarium/internal/mcp"
	"github.com/footprintai/containarium/pkg/version"
)

func main() {
	// Log to stderr so stdout stays clean for the MCP protocol stream.
	// Set this BEFORE LoadConfig so any config-load logging lands on
	// stderr too (the printUsage path logs to whatever's wired up).
	log.SetOutput(os.Stderr)

	// Read configuration from environment or config file
	config := mcp.LoadConfig()

	if config.ServerURL == "" {
		printUsage()
		log.Fatal("no server URL found: set CONTAINARIUM_SERVER_URL, or run `containarium login` (writes ~/.containarium/credentials.json with a default_server)")
