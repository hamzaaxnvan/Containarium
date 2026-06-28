package mcp

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
)

// Platform MCP tools for compose-autostart. Thin pass-throughs over
// the daemon's ComposeAutostartService gRPC, reached via grpc-gateway
// at POST /v1/tenants/{username}/compose/{verb}. Mirror of the
// agent-box MCP tools (which run INSIDE the LXC) for external agents
// that drive the platform from outside.
//
// Handlers return the daemon's JSON response verbatim — agents can
// parse it directly if they need structured data, or render the raw
// JSON for the human.
//
// Each tool requires `username` (which tenant LXC); enable/disable/
// status additionally require `dir`. Schemas mirror the proto's
// per-RPC required fields.

// ---- Client methods (typed wrappers) -------------------------------

type composeDiscoverReq struct {
	Username string   `json:"username"`
	Root     string   `json:"root,omitempty"`
	MaxDepth int32    `json:"maxDepth,omitempty"`
	Skip     []string `json:"skip,omitempty"`
	NoSkip   bool     `json:"noSkip,omitempty"`
}

type composeEnableReq struct {
	Username string `json:"username"`
	Dir      string `json:"dir"`
	Force    bool   `json:"force,omitempty"`
}

type composeDisableReq struct {
	Username string `json:"username"`
	Dir      string `json:"dir"`
}

// composeDispatch is the common shape for the POST verbs (discover /
// enable / disable). They all POST {body} to
// /v1/tenants/{username}/compose/<verb>. Returns the raw response
// body so handlers can pass it through to the agent verbatim.
func (c *Client) composeDispatch(verb, username string, body any) (json.RawMessage, error) {
	if username == "" {
		return nil, fmt.Errorf("username is required")
	}
	path := "/v1/tenants/" + url.PathEscape(username) + "/compose/" + verb
	resp, err := c.doRequest("POST", path, body)
	if err != nil {
		return nil, fmt.Errorf("compose %s: %w", verb, err)
	}
	return json.RawMessage(resp), nil
}

// composeStatus is a GET (no body) — the proto exposes Status as GET
// for cheap polling.
func (c *Client) composeStatus(username, dir string) (json.RawMessage, error) {
	if username == "" {
		return nil, fmt.Errorf("username is required")
	}
	if dir == "" {
		return nil, fmt.Errorf("dir is required")
	}
	path := "/v1/tenants/" + url.PathEscape(username) + "/compose/status?dir=" + url.QueryEscape(dir)
	resp, err := c.doRequest("GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("compose status: %w", err)
	}
	return json.RawMessage(resp), nil
}

// ---- Handlers ------------------------------------------------------

func handleComposeDiscoverPlatform(client API, args map[string]interface{}) (string, error) {
	username, _ := args["username"].(string)
	req := composeDiscoverReq{
		Username: username,
		Root:     getStringArg(args, "root", ""),
