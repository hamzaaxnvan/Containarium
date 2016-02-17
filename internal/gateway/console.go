package gateway

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/ws"
)

// ConsoleHandler handles WebSocket live-attach connections to a VM
// instance's serial console via Incus's ConsoleInstance. Unlike
// TerminalHandler (an exec'd shell, JSON-framed for browser xterm.js
// resize support), a serial console is a dumb byte pipe — no PTY
// negotiation, no resize after attach — so this relays raw binary
// WebSocket frames directly.
type ConsoleHandler struct {
	upgrader    websocket.Upgrader
	incusClient incus.InstanceServer
}

// NewConsoleHandler creates a new console handler backed by the local
// Incus unix socket.
func NewConsoleHandler() (*ConsoleHandler, error) {
	client, err := incus.ConnectIncusUnix("", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Incus: %w", err)
	}

	return &ConsoleHandler{
		upgrader:    newConsoleUpgrader(),
		incusClient: client,
	}, nil
}

// newConsoleUpgrader builds the console-attach upgrader. It deliberately
// reuses the terminal upgrader so both WebSocket routes share one Origin rule
// (see checkTerminalOrigin), one refusal body and one status/header shape.
// There is no intentional difference between the two routes.
func newConsoleUpgrader() websocket.Upgrader {
	return newTerminalUpgrader()
}

// parseConsoleUsername extracts the username from a
// /v1/containers/{username}/console-attach path. Returns "" if the path
// doesn't match that shape.
func parseConsoleUsername(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// ["v1", "containers", "{username}", "console-attach"]
	if len(parts) != 4 || parts[0] != "v1" || parts[1] != "containers" || parts[3] != "console-attach" {
		return ""
	}
	return parts[2]
}

// HandleConsole upgrades the request to a WebSocket and relays raw bytes
// between the caller and the instance's serial console. Callers must
// already be authenticated and tenant-authorized (see
// authorizeTenantWSAccess in gateway.go) — matching TerminalHandler's
