// Package connectcore holds the transport-agnostic core of the
// `containarium connect` flow — the container DTO, SSH-target resolution,
// and ssh-argv assembly — shared by the CLI verb (internal/cmd/connect.go)
// and the MCP `connect` tool (internal/mcp/connect.go).
//
// Only the HTTP transport differs between the two surfaces (the CLI uses a
// token+server client; the MCP server reuses its own daemon client), so
// the two thin REST calls live at each call site while all the resolve
// logic lives here — no duplicated target/state/argv logic.
package connectcore

import (
	"fmt"
	"strconv"
	"strings"
)

// Container is the subset of the daemon's Container we need to build an
// SSH target. grpc-gateway emits camelCase (UseProtoNames=false), so the
// json tags are camelCase.
type Container struct {
	Username string `json:"username"`
	State    string `json:"state"`
	SshHost  string `json:"sshHost"`
	Network  struct {
		IpAddress string `json:"ipAddress"`
	} `json:"network"`
}

// GetContainerResponse wraps Container as the GET /v1/containers/{box}
// response does.
type GetContainerResponse struct {
	Container Container `json:"container"`
}

// AuthorizeKeyRequest is the POST /v1/containers/{box}/ssh-keys body. The
// {box} path segment carries the container; the body only needs the key.
// The wire accepts either case; camelCase matches the daemon's emit.
type AuthorizeKeyRequest struct {
	SshPublicKey string `json:"sshPublicKey"`
}

// Target is a resolved SSH destination.
