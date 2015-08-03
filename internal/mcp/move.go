package mcp

import (
	"encoding/json"
	"fmt"
)

// handleMoveContainer is the MCP tool handler for `move_container`.
// Thin wrapper over the daemon's POST /v1/containers/{username}/move —
// the actual orchestration (snapshot, pre-copy, cutover, route swap)
// runs daemon-side. The MCP server just relays the call and renders
// the response in a human-readable shape that the agent can show
// directly to the operator.
func handleMoveContainer(client API, args map[string]interface{}) (string, error) {
	username, ok := args["username"].(string)
	if !ok || username == "" {
		return "", fmt.Errorf("username is required")
	}
	target, ok := args["target_backend_id"].(string)
	if !ok || target == "" {
		return "", fmt.Errorf("target_backend_id is required")
	}

	body := map[string]interface{}{
		"username":          username,
		"target_backend_id": target,
	}
