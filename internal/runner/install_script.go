// Package runner provisions Containarium boxes as ephemeral GitHub
// Actions self-hosted runners.
//
// Two entry points share one orchestration helper:
//
//   - internal/cmd/runner.go      → CLI verbs (containarium runner provision/list/remove)
//   - internal/mcp/tools.go       → MCP tools  (provision_runners/list_runners/remove_runner)
//
// Per CLAUDE.md: the CLI is canonical. The MCP tools are thin wrappers
