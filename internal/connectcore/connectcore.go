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
type Target struct {
	User string
	Host string
	Port int
}

// IsRunning reports whether a box can accept connections. protojson emits
// the enum identifier ("CONTAINER_STATE_RUNNING"); we also accept the
// friendly "Running" defensively.
func IsRunning(state string) bool {
	return state == "CONTAINER_STATE_RUNNING" || strings.EqualFold(state, "running")
}

// IsTransientState reports whether a box is in a short-lived bring-up state
// (still being created/provisioned) that will reach RUNNING on its own. A
// caller that just created a box and immediately wants to connect should
// wait these out rather than failing — waiting succeeds, whereas "start it
// first" is wrong (you can't start a box that's already coming up). protojson
// emits the enum identifier; the friendly form is accepted defensively.
func IsTransientState(state string) bool {
	switch {
	case state == "CONTAINER_STATE_CREATING", strings.EqualFold(state, "creating"):
		return true
	case state == "CONTAINER_STATE_PROVISIONING", strings.EqualFold(state, "provisioning"):
		return true
	}
	return false
}

// PrettyState trims the proto enum prefix for human-facing messages.
func PrettyState(state string) string {
	s := strings.TrimPrefix(state, "CONTAINER_STATE_")
	if s == "" {
		return "unknown"
	}
	return strings.ToLower(s)
}

// BuildTarget derives user@host:port, preferring overrides, then the
// daemon-owned ssh_host (FootprintAI/Containarium#452), then the container
// IP. Returns an actionable error when neither a host nor an IP is known
// yet (box still being placed).
func BuildTarget(c *Container, userOverride, hostOverride string, port int) (Target, error) {
	user := userOverride
