package mcp

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServerCreation tests MCP server creation
func TestServerCreation(t *testing.T) {
	config := &Config{
		ServerURL: "http://localhost:8080",
		JWTToken:  "test-token",
		Debug:     false,
	}

	server, err := NewServer(config)
	require.NoError(t, err)
	assert.NotNil(t, server)
	assert.Equal(t, config, server.config)
	assert.NotNil(t, server.client)
	// 30 base (+check_for_updates +upgrade_backend +get_upgrade_status, #354) + 3 runner-provision + 4 compose-autostart (#325) + 2 recipes + 3 backups + connect (#453) + 2 agent-skills (#562) + call_agent (#570) + 2 crews (#584) + delete_route + install_zap (#960) + set_metrics_export + get_metrics_export (#1069) + verify_backup (#1159) + list_passthrough_routes + add_passthrough_route + delete_passthrough_route (#1550) + security_sentry_status (#1640) + list_bad_destinations + add_bad_destination + remove_bad_destination (#1641) + list_security_sentry_findings + resolve_security_sentry_finding (#1643) + 4 code run/attach/status/stop (#1698) + add_collaborator + list_collaborators + remove_collaborator (#1145) + 6 tracker_get_issue/list_issues/get_change/comment/claim/set_labels (#1922) + tracker_submit_change (#1923) + tracker_route_list (#2021) + tracker_create_issue (#2024) + crew_logs (#2096) + mint_gateway_token (#1726) + code_runs + code_logs (#2123) + list_agent_engines (#2223) + network_guard_status (#2353) + guardrail_policy_get (#2368) + code_egress_policy (#2378) + terminate_user_sessions (#2522) + 3 security_process_policy_get/set/release (#2666) + install_clamav (#2659) + sprint_board + sprint_list + set_ssh_keys (#2945) + code_pending + code_answer (#2184) + 4 native Tenant tools (#3165).
	assert.Len(t, server.tools, 109, "Should have 109 tools registered")
}

// TestServerTools tests tool registration
func TestServerTools(t *testing.T) {
	config := &Config{
		ServerURL: "http://localhost:8080",
		JWTToken:  "test-token",
	}

	server, err := NewServer(config)
	require.NoError(t, err)

	expectedTools := []string{
		"create_container",
		"list_containers",
		"get_container",
		"delete_container",
		"start_container",
		"stop_container",
		"get_metrics",
		"get_system_info",
	}

	// Check all expected tools are registered
	toolNames := make(map[string]bool)
	for _, tool := range server.tools {
		toolNames[tool.Name] = true
	}

	for _, expected := range expectedTools {
		assert.True(t, toolNames[expected], "Tool '%s' should be registered", expected)
	}
}

// TestHandleInitialize tests the initialize method
func TestHandleInitialize(t *testing.T) {
	config := &Config{
		ServerURL: "http://localhost:8080",
		JWTToken:  "test-token",
	}

	server, err := NewServer(config)
	require.NoError(t, err)

	req := &MCPRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params:  map[string]interface{}{},
	}

	resp := server.handleRequest(req)

	assert.Equal(t, "2.0", resp.JSONRPC)
	assert.Equal(t, 1, resp.ID)
	assert.Nil(t, resp.Error)
	assert.NotNil(t, resp.Result)

	// Check result structure
	result, ok := resp.Result.(map[string]interface{})
	require.True(t, ok)

	assert.Equal(t, "2024-11-05", result["protocolVersion"])
	assert.NotNil(t, result["serverInfo"])
	assert.NotNil(t, result["capabilities"])

	// Check server info — version comes from pkg/version (settable
	// via ldflags). Don't assert an exact value (would have to be
	// bumped on every release). Just confirm a non-empty version
	// string ships back.
	serverInfo := result["serverInfo"].(map[string]interface{})
	assert.Equal(t, "containarium-mcp-server", serverInfo["name"])
	assert.NotEmpty(t, serverInfo["version"], "serverInfo.version must be set from pkg/version")
}

// TestHandleToolsList tests the tools/list method
func TestHandleToolsList(t *testing.T) {
	config := &Config{
		ServerURL: "http://localhost:8080",
		JWTToken:  "test-token",
	}

	server, err := NewServer(config)
	require.NoError(t, err)

	req := &MCPRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	}

	resp := server.handleRequest(req)

	assert.Equal(t, "2.0", resp.JSONRPC)
	assert.Equal(t, 2, resp.ID)
	assert.Nil(t, resp.Error)

	// Check tools list
	result, ok := resp.Result.(map[string]interface{})
	require.True(t, ok)

	tools, ok := result["tools"].([]map[string]interface{})
	require.True(t, ok)
	// See TestServerCreation's tally comment for the running count — 109.
	assert.Len(t, tools, 109)

	// Check first tool structure
	firstTool := tools[0]
	assert.NotEmpty(t, firstTool["name"])
	assert.NotEmpty(t, firstTool["description"])
	assert.NotNil(t, firstTool["inputSchema"])

	// Verify input schema is valid
	schema := firstTool["inputSchema"].(map[string]interface{})
	assert.Equal(t, "object", schema["type"])
	assert.NotNil(t, schema["properties"])
}

// TestHandleMethodNotFound tests unknown method handling
func TestHandleMethodNotFound(t *testing.T) {
	config := &Config{
		ServerURL: "http://localhost:8080",
		JWTToken:  "test-token",
	}

	server, err := NewServer(config)
	require.NoError(t, err)

	req := &MCPRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "unknown/method",
	}

	resp := server.handleRequest(req)

	assert.Equal(t, "2.0", resp.JSONRPC)
	assert.Equal(t, 3, resp.ID)
	assert.NotNil(t, resp.Error)
	assert.Nil(t, resp.Result)

	// Check error
	assert.Equal(t, -32601, resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "Method not found")
}

// TestHandleToolsCallInvalidParams tests invalid parameters
func TestHandleToolsCallInvalidParams(t *testing.T) {
	config := &Config{
		ServerURL: "http://localhost:8080",
		JWTToken:  "test-token",
	}

	server, err := NewServer(config)
	require.NoError(t, err)

	tests := []struct {
		name   string
		params interface{}
	}{
		{
			name:   "missing name",
			params: map[string]interface{}{"arguments": map[string]interface{}{}},
		},
		{
			name:   "invalid name type",
			params: map[string]interface{}{"name": 123, "arguments": map[string]interface{}{}},
		},
	}
