package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// `containarium traffic` exposes the TrafficService over the CLI — the
// canonical surface per CLAUDE.md (the webui and MCP are other consumers of the
// same endpoints). These are thin read-only wrappers over the grpc-gateway REST
// routes generated from proto/containarium/v1/traffic.proto:
//
//	GET /v1/containers/{name}/connections          → connections
//	GET /v1/containers/{name}/connections/summary  → summary
//	GET /v1/containers/{name}/traffic/history      → history
//	GET /v1/traffic/history                        → find (every box, admin only)
//
// Server + token resolution mirrors the ssh/connect commands (pickSSHServer +
// the bearer token auto-filled by root's PersistentPreRunE), so `traffic` works
// against whatever server you're logged into without re-passing --server.
var (
	trafficServerFlag string
	trafficFormat     string
	trafficProtocol   string
	trafficDestIP     string
	trafficDestPort   uint32
	trafficLimit      int32
	trafficSince      time.Duration
	trafficFrom       string
	trafficTo         string
)

var trafficCmd = &cobra.Command{
	Use:   "traffic",
	Short: "Inspect a box's network traffic (connections, summary, history)",
	Long: `Inspect network traffic for one of your boxes.

Subcommands:
  connections <box>   active connections (source/dest IP, port, proto, bytes)
  summary <box>       per-box totals + top destinations
  history <box>       closed connections recorded in the traffic history
  find                which box connected to a destination (every box; admin)

Reads the platform daemon's TrafficService over its HTTP API, using the
server + token you logged in with (override with --server / --token).`,
}

var trafficConnectionsCmd = &cobra.Command{
	Use:     "connections <box>",
	Aliases: []string{"conns", "ls"},
	Short:   "List active connections for a box",
	Args:    cobra.ExactArgs(1),
	RunE:    runTrafficConnections,
}

var trafficSummaryCmd = &cobra.Command{
	Use:   "summary <box>",
	Short: "Show connection totals + top destinations for a box",
	Args:  cobra.ExactArgs(1),
	RunE:  runTrafficSummary,
}

var trafficHistoryCmd = &cobra.Command{
	Use:   "history <box>",
	Short: "List closed connections from the traffic history",
	Args:  cobra.ExactArgs(1),
	RunE:  runTrafficHistory,
}

var trafficFindCmd = &cobra.Command{
	Use:   "find --from <time> [--to <time>] [--dest-ip <ip>] [--dest-port <port>]",
	Short: "Find which box connected to a destination, across every box (admin)",
	Long: `Search the traffic history of every box on the backend, without naming
the box. Each row names the box that made the connection.

This reveals every tenant's connection history, so it needs an admin token.
--from is required; --to defaults to now; the window is at most 7 days. Both
ends are inclusive and are RFC3339 timestamps (e.g. 2026-10-20T10:00:00Z).
--dest-ip is an exact address match.`,
	Args: cobra.NoArgs,
	RunE: runTrafficFind,
}

func init() {
