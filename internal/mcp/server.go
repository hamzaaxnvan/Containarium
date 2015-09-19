package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/footprintai/containarium/pkg/version"
)

// Server implements the MCP (Model Context Protocol) server
type Server struct {
	config *Config
	client API
	tools  []Tool
}

// NewServer creates a new MCP server. The backend is selected by newBackend
// from the credential: a hosted-control-plane API token (`ctnr_…`) gets the
// cloud backend (host-level ops report "not available here"); anything else
// gets the OSS daemon backend.
func NewServer(config *Config) (*Server, error) {
	server := &Server{
		config: config,
		client: newBackend(config),
		tools:  []Tool{},
	}

	// Register all tools
	server.registerTools()

	// #1922 step 7 — operator-curated tool allow-list, e.g. "tracker_*"
	// for an in-box agent mounting this server with a narrowly-scoped
	// JWT. Empty (the default) leaves every registered tool available,
	// unchanged from before this existed.
	if config.MCPTools != "" {
		filtered, err := filterToolsByAllowlist(server.tools, config.MCPTools)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", EnvMCPTools, err)
		}
		server.tools = filtered
	}

	return server, nil
}

// Start starts the MCP server (reads from stdin, writes to stdout)
func (s *Server) Start() error {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)

	for scanner.Scan() {
		line := scanner.Bytes()

		if s.config.Debug {
			log.Printf("Received: %s", string(line))
		}

		response, reply := s.handleLine(line)
		if !reply {
			continue
		}
		if err := encoder.Encode(response); err != nil {
			log.Printf("Failed to encode response: %v", err)
			continue
		}

		if s.config.Debug {
			respJSON, _ := json.Marshal(response)
			log.Printf("Sent: %s", string(respJSON))
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner error: %w", err)
	}

	return nil
}

// handleLine turns one line read from the client into the reply to send, and
// says whether there is one. It exists so that decision is a pure function of
// the bytes in, testable without a pipe.
//
// A message with no "id" member is a NOTIFICATION. JSON-RPC 2.0 says the server
// must not reply to one, and MCP clients send them on every connection —
// notifications/initialized closes the handshake. This server used to run every
// line through handleRequest, which answered the unknown method
// notifications/initialized with an error whose id is null; Claude Code's client
// validates responses strictly, rejects id:null and drops the connection about a
// second after it opens, so the agent saw its MCP server as failed and had no
// tools (#2529). Notifications are now consumed silently.
//
// Only a message that is not valid JSON gets an id:null error: with no id to
// echo, JSON-RPC defines exactly that reply.
func (s *Server) handleLine(line []byte) (*MCPResponse, bool) {
	var request MCPRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return s.createErrorResponse(nil, -32700, "Parse error", err.Error()), true
	}

	// Presence, not value: "id": null is a request (discouraged, but a request),
	// a missing id is a notification. MCPRequest.ID cannot tell them apart.
	var members map[string]json.RawMessage
	if err := json.Unmarshal(line, &members); err == nil {
		if _, hasID := members["id"]; !hasID {
			if s.config.Debug {
				log.Printf("Notification %q: no reply", request.Method)
			}
			return nil, false
		}
	}

	return s.handleRequest(&request), true
}

// handleRequest handles an MCP request
func (s *Server) handleRequest(req *MCPRequest) *MCPResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	case "ping":
		// Clients use ping as a liveness check; the answer is an empty result.
		return &MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{}}
	default:
		return s.createErrorResponse(req.ID, -32601, "Method not found", fmt.Sprintf("Unknown method: %s", req.Method))
	}
}

// handleInitialize handles the initialize request
func (s *Server) handleInitialize(req *MCPRequest) *MCPResponse {
	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"protocolVersion": "2024-11-05",
