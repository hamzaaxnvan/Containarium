//go:build !windows

package agentbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Compose-autostart tools — Phase B of the compose-autostart
// design (docs/COMPOSE-AUTOSTART-DESIGN.md).
//
// In-box MCP tools that let an agent self-discover compose
// stacks in its tenant's $HOME and opt them in to systemd-user
// autostart so they survive a host reboot.
//
// All state lives in the LXC: the systemd-user units under
// ~/.config/systemd/user/, plus loginctl linger. No daemon
// involvement. Phase C lands a parallel daemon-RPC path; this
// phase intentionally ships first because agent self-protection
// is the highest-value primitive.
//
// Tools:
//   - compose_discover           — walk $HOME, return per-stack info
//   - compose_autostart_enable   — install + enable the user unit
//   - compose_autostart_disable  — stop + disable + remove
//   - compose_autostart_status   — single-stack query

const (
	composeUnitTemplate = "containarium-compose@.service"
	composeUnitPath     = ".config/systemd/user/" + composeUnitTemplate
	defaultMaxDepth     = 5
)

// defaultSkip is the built-in skip-list. Configurable per-call
// via the `skip` tool argument; bypass entirely via `no_skip`.
// Covers the directories most likely to contain vendored /
// test-fixture compose files that aren't real workloads.
var defaultSkip = []string{
	"node_modules", ".git", "vendor", "target", "dist",
	"build", ".cache", ".venv", "venv", "__pycache__",
}

// ComposeStack mirrors the proto ComposeStack shape from the
// design doc. JSON tags align with the MCP tool output and
// (eventually) the gRPC ComposeStack message.
type ComposeStack struct {
	ComposeDir        string `json:"compose_dir"`
	ComposeFile       string `json:"compose_file"`
	ComposeBin        string `json:"compose_bin"`
	RunningCount      int    `json:"running_count"`
	TotalCount        int    `json:"total_count"`
	AutostartEnabled  bool   `json:"autostart_enabled"`
	UnitName          string `json:"unit_name,omitempty"`
	ComposeModifiedAt string `json:"compose_modified_at,omitempty"`
	UnitModifiedAt    string `json:"unit_modified_at,omitempty"`
}

func registerComposeTools(s *server.MCPServer) {
	s.AddTool(composeDiscoverTool(), handleComposeDiscover)
	s.AddTool(composeEnableTool(), handleComposeEnable)
	s.AddTool(composeDisableTool(), handleComposeDisable)
	s.AddTool(composeStatusTool(), handleComposeStatus)
}

// ----- compose_discover ---------------------------------------------------

func composeDiscoverTool() mcp.Tool {
	return mcp.NewTool(
		"compose_discover",
		mcp.WithDescription(
			"Walk $HOME for docker-compose / compose YAML files and report each "+
				"as a stack: compose_dir, compose_file, compose_bin "+
				"(podman-compose|docker compose|podman compose), running_count + "+
				"total_count (so you can tell partial degradation from fully up/down), "+
				"autostart status, and the modification times of both compose file "+
				"and its autostart unit (so you can detect 'compose changed since "+
				"autostart was wired'). Read-only.",
		),
		mcp.WithString("root",
			mcp.Description("Walk root (default: $HOME)."),
		),
		mcp.WithNumber("max_depth",
			mcp.Description(fmt.Sprintf("Max walk depth (default %d).", defaultMaxDepth)),
		),
		mcp.WithArray("skip",
			mcp.Description("Directory basenames to skip (additive to the built-in list unless no_skip=true)."),
		),
		mcp.WithBoolean("no_skip",
			mcp.Description("Bypass the built-in skip list (and any 'skip' overrides) and walk every directory. Useful for one-off audits."),
		),
	)
}

func handleComposeDiscover(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	root, _ := args["root"].(string)
	if root == "" {
		root = homeDir()
	}
	maxDepth := defaultMaxDepth
	if d, ok := argInt(args, "max_depth"); ok {
		maxDepth = d
	}
	noSkip, _ := args["no_skip"].(bool)
	extraSkip := argStringSlice(args, "skip")

	skipSet := map[string]struct{}{}
	if !noSkip {
		for _, d := range defaultSkip {
			skipSet[d] = struct{}{}
		}
	}
	for _, d := range extraSkip {
		skipSet[d] = struct{}{}
	}

	stacks, err := discoverStacks(root, maxDepth, skipSet)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("compose_discover: %v", err)), nil
	}

	// Resolve compose_bin once per process — it's a system
	// property, not per-stack, and the lookup is the same
	// every call.
	bin := resolveComposeBin()
	for i := range stacks {
		stacks[i].ComposeBin = bin
		if bin != "" {
			r, t := composeServiceCounts(bin, stacks[i].ComposeDir)
			stacks[i].RunningCount, stacks[i].TotalCount = r, t
		}
		fillAutostartStatus(&stacks[i])
	}

	body, _ := json.MarshalIndent(map[string]any{"stacks": stacks}, "", "  ")
	return mcp.NewToolResultText(string(body)), nil
}

// ----- compose_autostart_enable -------------------------------------------

func composeEnableTool() mcp.Tool {
	return mcp.NewTool(
		"compose_autostart_enable",
		mcp.WithDescription(
			"Install + enable the systemd-user unit that keeps the compose stack "+
				"at `dir` running across reboots. Idempotent — re-running refreshes "+
				"the unit and verifies the stack is up. Enables loginctl linger so "+
				"the user-systemd starts at host boot regardless of login state. "+
				"OPT-IN: nothing happens to other compose dirs.",
		),
		mcp.WithString("dir",
			mcp.Description("Path to the compose directory (the one containing docker-compose.yml or compose.yml)."),
			mcp.Required(),
		),
		mcp.WithBoolean("force",
			mcp.Description("Re-install the unit even if already enabled. Use when the compose file changed since the last enable."),
		),
	)
}

func handleComposeEnable(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	dir, _ := args["dir"].(string)
	if dir == "" {
		return mcp.NewToolResultError("compose_autostart_enable: 'dir' is required"), nil
	}
	dir = expandUser(dir)
	force, _ := args["force"].(bool)

	abs, err := filepath.Abs(dir)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolve dir: %v", err)), nil
	}
	if !hasComposeFile(abs) {
		return mcp.NewToolResultError(fmt.Sprintf("no docker-compose.yml / compose.yml found under %s", abs)), nil
	}

	bin := resolveComposeBin()
	if bin == "" {
		return mcp.NewToolResultError("no compose runtime found on PATH (looked for podman-compose, docker compose, podman compose)"), nil
	}

	slug := stackSlug(abs)
	unitInstance := "containarium-compose@" + slug + ".service"

	if !force && unitEnabled(unitInstance) {
		return mcp.NewToolResultText(fmt.Sprintf(
			"already enabled: %s (use force=true to refresh)", unitInstance,
		)), nil
	}

	// 1. install the template if missing or refresh on force.
	if err := installComposeUnit(bin, force); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("install unit template: %v", err)), nil
	}
	// 2. enable linger so user-systemd starts at host boot.
	if err := enableLinger(); err != nil {
		// Not fatal — autostart still works if the user is
		// logged in; we surface the warning so operators can
		// fix it (likely needs root sudo, which agent-box may
		// not have).
		_ = err
	}
	// 3. daemon-reload, then enable + start.
	if err := systemctlUser("daemon-reload"); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("daemon-reload: %v", err)), nil
	}
	if err := systemctlUser("enable", "--now", unitInstance); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("enable %s: %v", unitInstance, err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf(
		"enabled: %s (compose_bin=%s, dir=%s)", unitInstance, bin, abs,
	)), nil
}

// ----- compose_autostart_disable ------------------------------------------

func composeDisableTool() mcp.Tool {
	return mcp.NewTool(
		"compose_autostart_disable",
		mcp.WithDescription(
			"Stop and disable the autostart unit for the compose stack at `dir`. "+
				"Does NOT stop the running containers — use the compose CLI for that. "+
				"Just removes the boot-time restart protection.",
		),
		mcp.WithString("dir",
			mcp.Description("Path to the compose directory."),
			mcp.Required(),
		),
	)
}

func handleComposeDisable(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	dir, _ := args["dir"].(string)
	if dir == "" {
		return mcp.NewToolResultError("compose_autostart_disable: 'dir' is required"), nil
	}
	dir = expandUser(dir)
	abs, _ := filepath.Abs(dir)
	slug := stackSlug(abs)
	unitInstance := "containarium-compose@" + slug + ".service"

	if !unitEnabled(unitInstance) {
		return mcp.NewToolResultText(fmt.Sprintf("not enabled: %s (nothing to do)", unitInstance)), nil
	}

	// disable --now stops + disables in one call. We don't
	// call podman-compose down; the design says disable only
	// touches autostart, not the running stack.
	if err := systemctlUser("disable", unitInstance); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("disable %s: %v", unitInstance, err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf(
		"disabled: %s (containers still running; use compose CLI to stop them)", unitInstance,
	)), nil
}

// ----- compose_autostart_status -------------------------------------------

func composeStatusTool() mcp.Tool {
	return mcp.NewTool(
		"compose_autostart_status",
		mcp.WithDescription(
			"Single-stack version of compose_discover: report the autostart + "+
				"running state of the compose stack at `dir` without walking "+
				"$HOME.",
		),
		mcp.WithString("dir",
			mcp.Description("Path to the compose directory."),
			mcp.Required(),
		),
	)
}

func handleComposeStatus(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	dir, _ := args["dir"].(string)
	if dir == "" {
		return mcp.NewToolResultError("compose_autostart_status: 'dir' is required"), nil
	}
	dir = expandUser(dir)
	abs, _ := filepath.Abs(dir)

	cf, ok := findComposeFile(abs)
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("no compose file under %s", abs)), nil
	}

	bin := resolveComposeBin()
	stack := ComposeStack{
		ComposeDir:  abs,
		ComposeFile: cf,
		ComposeBin:  bin,
	}
	if info, err := os.Stat(cf); err == nil {
		stack.ComposeModifiedAt = info.ModTime().UTC().Format(time.RFC3339)
	}
	if bin != "" {
		stack.RunningCount, stack.TotalCount = composeServiceCounts(bin, abs)
	}
	fillAutostartStatus(&stack)

	body, _ := json.MarshalIndent(stack, "", "  ")
	return mcp.NewToolResultText(string(body)), nil
}

// =================================================================
//  Helpers
// =================================================================

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return os.Getenv("HOME")
}

func expandUser(p string) string {
	if strings.HasPrefix(p, "~/") || p == "~" {
		return filepath.Join(homeDir(), strings.TrimPrefix(p, "~"))
	}
	return p
}

// argInt extracts a number arg as int. JSON numbers decode to
// float64 by default; tolerate either.
func argInt(args map[string]any, key string) (int, bool) {
	switch v := args[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	}
	return 0, false
}

func argStringSlice(args map[string]any, key string) []string {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
