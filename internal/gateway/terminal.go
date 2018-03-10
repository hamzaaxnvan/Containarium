package gateway

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/toolaccess"
	coreincus "github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/gorilla/websocket"
	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
)

// PeerTerminalProxy resolves the WebSocket URL for a container on a peer backend.
// Returns empty string if the container is local or peers are not configured.
type PeerTerminalProxy interface {
	// PeerTerminalURL returns the WebSocket proxy URL for a container on a remote peer.
	// Returns ("", nil) if the container is local.
	PeerTerminalURL(username, authToken string) (string, error)
}

// TerminalHandler handles WebSocket terminal connections via Incus exec
type TerminalHandler struct {
	upgrader    websocket.Upgrader
	incusClient incus.InstanceServer
	peerProxy   PeerTerminalProxy

	// Seams for the as_run_user guard (#3275). nil = read the Incus instance
	// directly; tests replace them.
	recordedToolAccess func(containerName string) (string, error)
	runUserExists      func(containerName string) (bool, error)
}

// asRunUserParam is the query parameter that opens the terminal as the box's
// unprivileged run user instead of its login user (#3275).
const asRunUserParam = "as_run_user"

// terminalRefusal is a pre-upgrade refusal of an as_run_user request.
type terminalRefusal struct {
	status int
	msg    string
}

// resolveTerminalUser decides which OS user the terminal logs in as. Without
// asRunUser it is the box's login user, exactly as before. With it, the box must
// have a tool-access level recorded and the run user must already exist: the
// platform provisions that user on the first run, and a terminal that created
// it would be a second provisioning path. A read failure refuses rather than
// falling back to the login user, so a sign-in is never silently done as the
// wrong user.
func (th *TerminalHandler) resolveTerminalUser(containerName, username string, asRunUser bool) (string, *terminalRefusal) {
	if !asRunUser {
		return username, nil
	}
	levelFn, existsFn := th.recordedToolAccess, th.runUserExists
	if levelFn == nil {
		levelFn = th.readToolAccess
	}
	if existsFn == nil {
		existsFn = th.checkRunUserExists
	}
	level, err := levelFn(containerName)
	if err != nil {
		log.Printf("terminal %s: read tool-access level: %v", containerName, err)
		return "", &terminalRefusal{http.StatusServiceUnavailable, "could not read the box's tool-access level; try again"}
	}
	if toolaccess.FromConfig(level) == pb.BoxToolAccess_BOX_TOOL_ACCESS_UNSPECIFIED {
		return "", &terminalRefusal{http.StatusConflict, "as_run_user: this box has no tool-access level recorded, so platform runs do not use a run user"}
	}
	exists, err := existsFn(containerName)
	if err != nil {
		log.Printf("terminal %s: check run user: %v", containerName, err)
		return "", &terminalRefusal{http.StatusServiceUnavailable, "could not check for the run user; try again"}
	}
	if !exists {
		return "", &terminalRefusal{http.StatusConflict, "as_run_user: the run user does not exist yet; it is created by the box's first platform run"}
	}
	return toolaccess.RunUser, nil
}

func (th *TerminalHandler) readToolAccess(containerName string) (string, error) {
	inst, _, err := th.incusClient.GetInstance(containerName)
	if err != nil {
		return "", err
	}
	return inst.Config[coreincus.ToolAccessKey], nil
}

// checkRunUserExists asks the box, with a non-interactive `id -u`, whether the
// run user is there. A non-zero exit is "no"; any failure to run it is an error.
func (th *TerminalHandler) checkRunUserExists(containerName string) (bool, error) {
	op, err := th.incusClient.ExecInstance(containerName, api.InstanceExecPost{
		Command:   []string{"id", "-u", toolaccess.RunUser},
		WaitForWS: true,
	}, &incus.InstanceExecArgs{Stdin: nil, Stdout: io.Discard, Stderr: io.Discard, DataDone: make(chan bool)})
	if err != nil {
		return false, err
	}
	if err := op.Wait(); err != nil {
		return false, err
	}
	ret, ok := op.Get().Metadata["return"].(float64)
	if !ok {
		return false, fmt.Errorf("exec reported no exit status")
	}
	return ret == 0, nil
}

// NewTerminalHandler creates a new terminal handler
func NewTerminalHandler() (*TerminalHandler, error) {
	// Connect to local Incus socket
	client, err := incus.ConnectIncusUnix("", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Incus: %w", err)
	}

	return &TerminalHandler{
		upgrader:    newTerminalUpgrader(),
		incusClient: client,
	}, nil
}

// Reasons a terminal upgrade is refused on origin grounds. They are
// written into the 403 body so the refusal is not mistaken for a tenant
// authorization failure (authorizeTenantWSAccess is never reached).
const (
	originRequired   = "origin required"
	originNotAllowed = "origin not allowed"
)

// validatedBearerKey is the request-context key under which the terminal
// route wrapper records the source of a bearer it has validated.
type validatedBearerKey struct{}

// withValidatedBearer records that the request's bearer, found at src, has
// passed ValidateToken. Call it only after validation succeeded.
func withValidatedBearer(ctx context.Context, src auth.TokenSource) context.Context {
	return context.WithValue(ctx, validatedBearerKey{}, src)
}

// validatedBearerSource returns the source recorded by withValidatedBearer,
// or TokenSourceNone when no validated bearer was recorded.
func validatedBearerSource(ctx context.Context) auth.TokenSource {
	if src, ok := ctx.Value(validatedBearerKey{}).(auth.TokenSource); ok {
		return src
	}
	return auth.TokenSourceNone
}

// checkTerminalOrigin applies the terminal WebSocket origin rule.
//
// The check defends against cross-site WebSocket hijacking: a page on
// another site makes the victim's browser open a socket carrying ambient
// credentials. This endpoint takes no cookies; the bearer must be supplied
// explicitly (Sec-WebSocket-Protocol or Authorization), which a cross-site
// page can neither read nor attach. Browsers always send Origin on
// upgrades, so an absent Origin plus an explicit bearer is a non-browser
// client (a service, CLI or MCP server) and is allowed. The deprecated
// ?token= form does not count. Token validation and the tenant check still
// run in the route closure before the upgrade.
//
// The exemption follows a bearer the route wrapper VALIDATED and recorded
// with withValidatedBearer, not a bearer that is merely present on the
// request, so it cannot be unlocked by any path that reaches the upgrader
// without ValidateToken.
func checkTerminalOrigin(r *http.Request) (ok bool, reason string) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		if src := validatedBearerSource(r.Context()); src == auth.TokenSourceSubprotocol || src == auth.TokenSourceAuthorizationHeader {
			return true, ""
		}
		return false, originRequired
	}
	for _, allowed := range getTerminalAllowedOrigins() {
		if origin == allowed {
			return true, ""
		}
	}
	return false, originNotAllowed
}

// newTerminalUpgrader builds the upgrader shared by the terminal routes.
func newTerminalUpgrader() websocket.Upgrader {
	return websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			ok, reason := checkTerminalOrigin(r)
			if !ok {
				log.Printf("WebSocket connection rejected: %s (origin=%q)", reason, r.Header.Get("Origin"))
			}
			return ok
		},
		// Surface the origin reason instead of gorilla's generic body.
		Error: func(w http.ResponseWriter, r *http.Request, status int, reason error) {
			// A custom Error replaces gorilla's default responder, which is
			// what sets this header; keep it so refusals stay RFC 6455-shaped.
			w.Header().Set("Sec-Websocket-Version", "13")
			msg := reason.Error()
			if status == http.StatusForbidden {
				if _, why := checkTerminalOrigin(r); why != "" {
					msg = why
				}
			}
			http.Error(w, msg, status)
		},
		// Phase 1.5 — advertise the bearer subprotocol so gorilla picks it
		// up during negotiation and emits the right Sec-WebSocket-Protocol
		// response header. Picking only this name means an upgrade where
		// the client offered a different application subprotocol won't be
		// confused with auth.
		Subprotocols:    []string{auth.WSSubprotocolBearer},
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
}

// getTerminalAllowedOrigins returns the list of allowed origins for WebSocket connections.
// Configurable via CONTAINARIUM_ALLOWED_ORIGINS environment variable (comma-separated).
// Defaults to localhost origins only for security.
func getTerminalAllowedOrigins() []string {
	envOrigins := os.Getenv("CONTAINARIUM_ALLOWED_ORIGINS")
	if envOrigins != "" {
		origins := strings.Split(envOrigins, ",")
		// Trim whitespace from each origin
		for i, origin := range origins {
			origins[i] = strings.TrimSpace(origin)
		}
		return origins
	}
	// Default to localhost only - secure by default
	return []string{
		"http://localhost:3000",
		"http://localhost:8080",
		"http://localhost",
	}
}

// TerminalMessage represents a message sent over WebSocket
type TerminalMessage struct {
	Type string `json:"type"` // "input", "output", "resize", "error"
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

// SetPeerProxy sets the peer terminal proxy for multi-backend routing.
func (th *TerminalHandler) SetPeerProxy(proxy PeerTerminalProxy) {
	th.peerProxy = proxy
}

// HandleTerminal handles WebSocket connections for container terminal
func (th *TerminalHandler) HandleTerminal(w http.ResponseWriter, r *http.Request) {
	// Extract username from URL path: /v1/containers/{username}/terminal
	path := r.URL.Path
	parts := strings.Split(path, "/")
	if len(parts) < 4 {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	username := parts[3] // /v1/containers/{username}/terminal
	if username == "" {
		http.Error(w, "Username required", http.StatusBadRequest)
		return
	}

