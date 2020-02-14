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
