package server

import (
	"context"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/agentengine"
	"github.com/footprintai/containarium/internal/auth"
	appconfig "github.com/footprintai/containarium/internal/config"
	"github.com/footprintai/containarium/internal/modelgateway"
	"github.com/footprintai/containarium/internal/tokenid"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Model-gateway provisioning for skill boxes (#674 design, productionization of
// the #737 prototype). When the daemon holds a provider API key, it serves the
// model-gateway (internal/modelgateway) on its HTTP port and provisions each
// skill box to route its model calls through it: the box gets a short-lived,
// per-skill *gateway token* and the SDK base-URL env, so the real key never
// lives in a box and every call is metered per tenant/skill. This is an env
// change in the box, not a code change — the agent-runtime engines already honor
// these vars (Claude/OpenAI base-URL; Gemini via CONTAINARIUM_MODEL_GATEWAY_URL).

// gatewayProvisioning is the daemon-resolved config the AgentSkillServer needs
// to mint a box's gateway token and seed its env. nil ⇒ no provider key
// configured ⇒ boxes run in direct mode (the OSS/self-hosted default).
type gatewayProvisioning struct {
	// engines is the engine-resolution view of the gateway (#2222): the
	// default provider, which providers the daemon holds a global key for,
	// and how to check a per-owner key. provisionSkillBoxWith calls
	// agentengine.Resolve(ctx, skill.GetEngine(), keyOwner, &engines) once per
	// run — this is the ONLY thing that decides which provider a run's
	// gateway token is bound to; nothing else in this file picks a provider.
	engines  agentengine.Gateway
	httpPort int    // the daemon HTTP port the box dials (resolved to the host's default-route IP in-box)
	secret   []byte // shared HMAC secret (daemon jwt.secret) — signs the gateway token

	// models reads a provider's upstream model list (#2229), on the SAME key
	// a run's gateway token would spend. nil in direct mode and on any daemon
	// built before this field — provisionSkillBoxWith treats that as "can't
	// check" exactly like an unsupported provider, never as a refusal.
	// *modelgateway.Gateway satisfies this; a narrow interface (not the
	// concrete type) so a test can fake it with a call recorder instead of
	// standing up a real Gateway.
	models modelLister

	// egressCIDR is the host the box reaches the gateway on, as a /32 (the LXC
	// bridge gateway IP — also the daemon API + DNS). When set, the skill box's
	// egress policy allows ONLY this host (+ peers) for model calls and DROPS the
	// direct provider domains — so a box can't bypass the gateway (#674 inc 4).
	egressCIDR string
}

// modelLister is the one method RunAgentSkill's model-ceiling check (#2229)
// needs from the model gateway — see gatewayProvisioning.models.
type modelLister interface {
	ListModels(ctx context.Context, keyOwner, provider string) ([]string, error)
}

// gatewayProviderEnv is the per-provider env contract the agent-runtime engines
// read. urlSuffix is appended to the daemon HTTP base to form the SDK base URL;
// gemini takes the BARE base (its engine appends the /v1/model/gemini path).
type gatewayProviderEnv struct {
	urlVar    string
	tokenVar  string
	urlSuffix string
}

var gatewayProviderEnvs = map[string]gatewayProviderEnv{
	"anthropic": {urlVar: "ANTHROPIC_BASE_URL", tokenVar: "ANTHROPIC_AUTH_TOKEN", urlSuffix: "/v1/model/anthropic"}, // #nosec G101 -- env var names, not credential values
	// tokenVar is CODEX_API_KEY, not OPENAI_API_KEY (#2256): the Codex CLI
	// (spawned by @openai/codex-sdk, the only consumer of the "openai"
	// provider) documents CODEX_API_KEY as the variable a headless/non-
	// interactive run reads with no `codex login` step; OPENAI_API_KEY set as
	// a bare env var is an undocumented fallback OpenAI's own docs call
	// insufficient on its own (it expects piping into `codex login
	// --with-api-key`, which caches a session file this box never gets).
	// gatewayProviderKeysFromEnv's DAEMON-side OPENAI_API_KEY (the operator's
	// real key, read once at daemon startup) is a different, unrelated
	// variable and is unaffected by this — this is only the name exported
	// INTO the box's gateway.env.
	"openai": {urlVar: "OPENAI_BASE_URL", tokenVar: "CODEX_API_KEY", urlSuffix: "/v1/model/openai"},             // #nosec G101 -- env var names, not credential values
	"gemini": {urlVar: "CONTAINARIUM_MODEL_GATEWAY_URL", tokenVar: "CONTAINARIUM_GATEWAY_TOKEN", urlSuffix: ""}, // #nosec G101 -- env var names, not credential values
}

// mintGatewayToken mints a per-skill gateway token bound to this box's tenant +
// skill + provider, expiring with the in-box token (agentTokenTTL). provider
// is the run's RESOLVED provider (#2222, agentengine.Resolve's output) — a
// crew whose members resolve to two different engines mints two tokens here,
// each bound to its own member's provider, never the daemon's single default.
//
// runID binds the token to one skill run (#1817) and the returned MintedID is
// what lets the run's exit revoke it: without the jti the issuer would have to
// re-parse its own token to kill it.
//
// keyOwner is stamped as the token's key_owner claim (#2134) so the gateway
