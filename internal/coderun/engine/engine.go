// Package engine makes the coding agent and its credential source typed
// choices in `containarium code` (#1727), instead of the Claude-only,
// tenant-secret-only path #1673/#1674 shipped.
//
// The split follows what is actually engine-specific. The resumable reader —
// `code run`/`attach`/`status`/`stop`, internal/coderun — only ever sees bytes
// and byte offsets, so it is engine-agnostic and is NOT touched here. Three
// things do differ per engine, and they are this package's whole surface:
//
//   - InstallScript: how the binary lands on the box.
//   - VerifyScript: how the install proves the box can actually reach a model.
//   - RunCommand: the command process_start spawns.
//
// Credentials are the second axis. A `secret` source is the shipped behaviour
// (a tenant secret reaching the run through the box's own shell environment);
// a `gateway` source is #1726's scoped, short-lived gateway token, so the box
// holds no provider key at all. The axes are independent — an engine is
// constructed WITH a credential source (For) rather than being handed one per
// call, which is what keeps RunCommand's signature down to the three arguments
// that vary per run.
//
// Design: docs/architecture/workspace-own-inference-key.md (cloud repo),
// component 3 and contract C4.
package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/footprintai/containarium/internal/coderun"
)

// Name identifies a coding engine. A string type rather than a bare string so
// a mistyped literal is a compile error at every call site, and so the JSON
// record in code.json has one vocabulary (CLAUDE.md: use the type system).
type Name string

const (
	// NameClaude is Claude Code, the engine `containarium code` has always
	// installed. It is the DEFAULT for --engine, which is what makes #1727 a
	// no-behaviour-change addition for existing users.
	NameClaude Name = "claude"
	// NamePi is pi (https://pi.dev, @earendil-works/pi-coding-agent), a
	// local-first agent that runs inside the box. See docs/integrations/pi.md.
	NamePi Name = "pi"
	// NameCodex is OpenAI's Codex CLI (https://developers.openai.com/codex/cli,
	// npm package @openai/codex) — #2273, parallel to claude and pi. A
