//go:build !windows

// Package agentbox is the in-the-box MCP server library — the tools agents
// use when working on a single Containarium box. Tools are registered onto
// an mcp-go server.MCPServer instance by the agent-box binary.
//
// Tool taxonomy (v0):
//
//   - shell_exec      — run a shell command, capture stdout/stderr/exit code
//   - read_file       — read a file (byte range, head=N lines, or tail=N lines)
//   - write_file      — write a file atomically
//   - list_directory  — enumerate a directory's entries
//   - move_file       — rename/move a file or directory
//   - delete_file     — remove a single file (refuses directories)
//   - tail_log        — watch a file for new appends, bounded by follow_seconds
//   - process_start   — spawn a long-running process; output captured to a log file
//   - process_list    — list managed processes with PID, command, liveness
//   - process_kill    — SIGTERM (or SIGKILL with force) and reap
//
// Resource taxonomy (v0):
//
//   - containarium://ci-context — JSON metadata about the current CI run
//     (PR, commit, failing test), populated by the containarium-run GitHub
//     Action. Empty stub when the box isn't being used for CI.
//   - containarium://ci-prompt  — static markdown playbook telling agents
//     how to debug a failing CI run inside this box. Paired with
//     ci-context: ci-context is the data, ci-prompt is the behavior.
//
// File-ops tools enforce a sandbox in this order:
//  1. AGENTBOX_ROOT env var (strict floor when set).
//  2. MCP Roots advertised by the client via roots/list (used as the
//     sandbox when AGENTBOX_ROOT is unset). Falls back gracefully if
//     the client doesn't support the capability.
//  3. Unconstrained (only when both are absent).
//
// More tools (provision_postgres, deploy_app, snapshot, etc.) land in
// subsequent commits.
package agentbox

import "github.com/mark3labs/mcp-go/server"

// RegisterTools wires every agentbox tool into the given MCPServer. Called
// once at startup by cmd/agent-box. Each tool is implemented in its own
// file (shell.go, files.go, …) and registers itself via a Register*
