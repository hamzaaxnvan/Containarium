// Package agentengine resolves which agent engine (and which gateway
// provider) one skill run uses, given the manifest's own choice and the
// daemon's gateway state. It is the one place this rule lives (#2222,
// design: docs/architecture/agent-router.md) — every launch path a run can
// take (push run, crew member, pull-queue worker) and the status surface
// that reports readiness both call Resolve, so what the daemon reports as
// ready and what a run actually does cannot drift apart.
//
// This package is pure: Resolve takes an injected KeyResolver rather than
// reaching into a secrets store itself, so the whole decision table is a
// table-driven unit test with no database, no daemon, and no box.
package agentengine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/footprintai/containarium/internal/gatewayprovider"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Parse resolves a manifest/flag engine name ("claude", "codex", "gemini" —
// case-insensitive, trimmed) to its typed value. Unknown or empty input is an
// error listing the valid names — never a silent fall back to UNSPECIFIED. A
// skill that leaves the field unset carries AGENT_ENGINE_UNSPECIFIED directly
// (the proto zero value); Parse is for turning a NAME into that value, so it
// deliberately does not accept "" as meaning UNSPECIFIED.
func Parse(s string) (pb.AgentEngine, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "claude":
		return pb.AgentEngine_AGENT_ENGINE_CLAUDE, nil
	case "codex":
		return pb.AgentEngine_AGENT_ENGINE_CODEX, nil
	case "gemini":
		return pb.AgentEngine_AGENT_ENGINE_GEMINI, nil
	default:
		return pb.AgentEngine_AGENT_ENGINE_UNSPECIFIED, fmt.Errorf("unknown agent engine %q (one of: %s)", s, strings.Join(Names(), ", "))
	}
}

// All lists every concrete (non-UNSPECIFIED) engine, in enum order.
func All() []pb.AgentEngine {
	return []pb.AgentEngine{
		pb.AgentEngine_AGENT_ENGINE_CLAUDE,
		pb.AgentEngine_AGENT_ENGINE_CODEX,
		pb.AgentEngine_AGENT_ENGINE_GEMINI,
	}
}

// Names lists every engine Parse accepts, for error messages and flag help.
func Names() []string {
	out := make([]string, 0, len(All()))
	for _, e := range All() {
		out = append(out, EnvValue(e))
	}
	sort.Strings(out)
	return out
}

// EnvValue is the exact string CONTAINARIUM_AGENT_ENGINE carries for e, i.e.
// the value the agent-runtime's own pickEngine switch matches on
// (agent-runtime/src/index.ts). "" for UNSPECIFIED — callers use that as the
// "do not export this variable" signal rather than a literal value.
func EnvValue(e pb.AgentEngine) string {
	switch e {
	case pb.AgentEngine_AGENT_ENGINE_CLAUDE:
		return "claude"
	case pb.AgentEngine_AGENT_ENGINE_CODEX:
		return "codex"
	case pb.AgentEngine_AGENT_ENGINE_GEMINI:
		return "gemini"
	default:
		return ""
	}
}

// Provider is the gateway provider e speaks. ok=false for UNSPECIFIED (it
// names no provider of its own — Resolve fills one in from the gateway's
// default) and for any engine this package doesn't know.
func Provider(e pb.AgentEngine) (name string, ok bool) {
	switch e {
	case pb.AgentEngine_AGENT_ENGINE_CLAUDE:
		return "anthropic", true
	case pb.AgentEngine_AGENT_ENGINE_CODEX:
		return "openai", true
	case pb.AgentEngine_AGENT_ENGINE_GEMINI:
		return "gemini", true
	default:
