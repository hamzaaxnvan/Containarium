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
		return "", false
	}
}

// ForProvider inverts Provider. UNSPECIFIED for a provider no engine speaks
// (e.g. an operator-registered OpenAI-compatible upstream with its own
// provider name) — see docs/architecture/agent-router.md "What changes at
// 10x" for engines whose provider is not fixed.
func ForProvider(provider string) pb.AgentEngine {
	for _, e := range All() {
		if p, _ := Provider(e); p == provider {
			return e
		}
	}
	return pb.AgentEngine_AGENT_ENGINE_UNSPECIFIED
}

// KeyResolver reports whether a usable provider key exists for one key
// owner, without ever handing the key's value back to this package — Resolve
// only ever inspects the boolean. Any type satisfying modelgateway.KeyResolver
// (KeyFor(ctx, keyOwner, provider) (string, bool)) already implements this.
type KeyResolver interface {
	KeyFor(ctx context.Context, keyOwner, provider string) (string, bool)
}

// Gateway is the daemon's model-gateway state, as Resolve needs to see it. A
// nil *Gateway means direct mode (the daemon serves no gateway): boxes run
// with whatever credential their own environment supplies, and Resolve never
// refuses a run in that mode.
type Gateway struct {
	// DefaultProvider is what AGENT_ENGINE_UNSPECIFIED resolves to — today's
	// gatewayPrimaryProvider(keys): the first of anthropic/openai/gemini the
	// daemon holds a global key for. Empty if the daemon holds no global key
	// at all (gateway still "enabled" via an operator-registered upstream).
	DefaultProvider string
	// GlobalProviders is the set of providers the daemon holds a key for in
	// its own environment (not per key-owner). A provider in this set is
	// ready for every caller, with no per-owner lookup needed.
	GlobalProviders map[string]bool
	// Keys resolves a per-owner key when GlobalProviders doesn't already
	// cover the provider. Nil when the daemon has no per-owner store (no
	// Postgres) — then only GlobalProviders can make a provider ready.
	Keys KeyResolver
	// NoGlobalFallback mirrors modelgateway.Config.DisableGlobalKeyFallback:
	// for a run with a key owner, GlobalProviders no longer makes a provider
	// ready and no longer picks the default — only the owner's own key does.
	// Without it a run is admitted on a daemon-global key that the gateway will
	// then refuse to spend, and fails inside the box instead of up front.
	NoGlobalFallback bool
}

// defaultProviderOrder is the order an unspecified engine is matched to a
// provider when the choice is per owner (NoGlobalFallback); the same order the
// daemon uses to pick its global default.
var defaultProviderOrder = []string{"anthropic", "openai", "gemini"}

// globalCovers reports whether the daemon-global key makes provider ready for
// this caller.
func (g *Gateway) globalCovers(provider, keyOwner string) bool {
	if g.NoGlobalFallback && keyOwner != "" {
		return false
	}
	return g.GlobalProviders[provider]
}

func (g *Gateway) ownerHasKey(ctx context.Context, keyOwner, provider string) bool {
	if g.Keys == nil {
		return false
	}
	_, ok := g.Keys.KeyFor(ctx, keyOwner, provider)
	return ok
}

// Resolved is the engine and provider one run actually uses.
type Resolved struct {
	// Engine is never a value Parse would reject; it IS AGENT_ENGINE_UNSPECIFIED
	// exactly when no CONTAINARIUM_AGENT_ENGINE should be exported for this run
	// (direct mode with no engine named).
	Engine pb.AgentEngine
	// Provider is the gateway provider the run's token is bound to; "" in
	// direct mode, where there is no gateway to bind a token to.
	Provider string
	// Default is true when `want` was UNSPECIFIED and this result came from
	// the gateway's default provider (or, in direct mode, from there being no
	// engine named at all) rather than the manifest naming one explicitly.
	// Callers use this to decide whether to export CONTAINARIUM_AGENT_MODEL:
	// only when Default is false (Q1, docs/product/agent-router.md).
	Default bool
}

// NotReadyError is returned when a named engine's provider has no resolvable
