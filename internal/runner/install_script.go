// Package runner provisions Containarium boxes as ephemeral GitHub
// Actions self-hosted runners.
//
// Two entry points share one orchestration helper:
//
//   - internal/cmd/runner.go      → CLI verbs (containarium runner provision/list/remove)
//   - internal/mcp/tools.go       → MCP tools  (provision_runners/list_runners/remove_runner)
//
// Per CLAUDE.md: the CLI is canonical. The MCP tools are thin wrappers
// over the same Go function that the CLI handler calls. Don't make
// the MCP tool talk to a different code path.
package runner

import _ "embed"

// To resync the embedded payload with hacks/runner/install.sh after
// editing the source, run:
//
//   go generate ./internal/runner/...
