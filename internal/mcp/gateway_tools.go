package mcp

import (
	"fmt"
	"strings"
)

// gatewayTools is the MCP-side catalog for the model gateway's mint surface
// (#1726). Pulled into tools.go's registration list via gatewayTools(),
// mirroring kmsTools().
//
// Per CLAUDE.md's CLI-first rule this is a THIN wrapper: one tool, over the same
// ModelGatewayService.MintGatewayToken RPC `containarium gateway mint` calls. It
// adds no capability the shell does not already have.
//
// Deliberately absent: the key verbs. Registering or removing a key owner's REAL
// upstream provider key is an operator/control-plane action gated by
// gateway:admin, and an agent has no business holding a credential it could spend
// as somebody else. An agent minting a scoped, expiring token for its own box is
// the whole point; an agent writing the key that token draws on is not.
func gatewayTools() []Tool {
	return []Tool{
		{
			Name: "mint_gateway_token",
			Description: "Mint a short-lived, scoped model-gateway token for a box " +
				"you own, so an engine in that box can call models without ever " +
				"holding a real provider key. The token is bound to the box, the " +
				"provider, the resolved key owner (the box's cloud-org attribution " +
				"when stamped, else its owning username) and optionally one run and " +
				"a model ceiling; its TTL is capped server-side and it can be revoked " +
				"by its token_id before expiry. Use dry_run to check that a mint " +
				"WOULD succeed without issuing anything. Mirrors " +
				"`containarium gateway mint`.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"box": map[string]interface{}{
						"type":        "string",
						"description": "The box to mint for, as its tenant username or <username>-container. Must be a box you own.",
					},
					"provider": map[string]interface{}{
						"type":        "string",
						"description": "Upstream provider name, e.g. anthropic, openai, gemini, gemini-openai, kafeido.",
					},
					"run_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional. Binds the token to one run, so the run's exit can revoke exactly this token.",
					},
					"ttl": map[string]interface{}{
						"type":        "string",
						"description": "Optional requested lifetime as a Go duration (e.g. 30m, 2h). Capped server-side; omitted takes the default.",
					},
					"allowed_models": map[string]interface{}{
						"type":        "array",
