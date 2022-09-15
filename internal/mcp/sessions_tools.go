package mcp

import (
	"fmt"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// SSH session control (#2522): terminate_user_sessions wraps the same client
// call `containarium sessions terminate` makes.

func sessionTools() []Tool {
	return []Tool{{
		Name: "terminate_user_sessions",
		Description: "End every live SSH session a user holds on the backend: their own box and " +
			"every box they reach as a collaborator, in the box and on the host jump account. " +
			"Deletes nothing and leaves authorized_keys alone, so the user can connect again. " +
			"An admin may name any user, anyone else only themselves. " +
			"Same function as `containarium sessions terminate --user`.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"username": map[string]interface{}{
					"type":        "string",
					"description": "User whose sessions to end (the box owner name or collaborator username).",
				},
			},
			"required": []string{"username"},
		},
		Handler: handleTerminateUserSessions,
	}}
}

func handleTerminateUserSessions(api API, args map[string]interface{}) (string, error) {
	username := getStringArg(args, "username", "")
	if username == "" {
		return "", fmt.Errorf("username is required")
	}
	resp, err := api.TerminateUserSessions(username)
	if err != nil {
		return "", fmt.Errorf("failed to terminate sessions: %w", err)
	}
	return fmt.Sprintf("✅ Terminated sessions for %s: %d account(s) with live processes ended across %d box(es).",
		username, resp.GetTerminatedSessions(), resp.GetBoxesTouched()), nil
}

// newSessionsAPI builds the CLI's typed client from this MCP client's
// transport, base URL and current token. A variable so the tool test can
// prove the MCP path goes through the shared client.
var newSessionsAPI = func(c *Client) (client.SessionsAPI, error) {
	if c.tlsConfigErr != nil {
