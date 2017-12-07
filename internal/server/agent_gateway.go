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
// spends that owner's registered key; "" mints no claim and the token resolves
// through the daemon-global key exactly as before. Callers resolve it with
// runKeyOwner, which only ever returns a validated owner or "".
//
// model (#2229) is the skill manifest's own pinned model, already confirmed
// (by the caller, before this mint) to be one the resolved provider actually
// serves when a check was possible. Empty means the skill pins none — the
// token carries no AllowedModels ceiling, the gateway's own default applies,
// unchanged pre-#2229 behavior. A skill with one DOES get a real ceiling: the
// token can spend on exactly that model and no other, enforced at the
// gateway (internal/modelgateway/gateway.go), not just advisory.
func (g *gatewayProvisioning) mintGatewayToken(tenant, skillID, runID, keyOwner, provider, model string) (string, tokenid.MintedID, error) {
	var allowedModels []string
	if model != "" {
		allowedModels = []string{model}
	}
	return modelgateway.MintTokenWithID(g.secret, modelgateway.GatewayClaims{
		Tenant:        tenant,
		SkillID:       skillID,
		Provider:      provider,
		AllowedModels: allowedModels,
		RunID:         runID,
		KeyOwner:      keyOwner,
	}, agentTokenTTL)
}

// runKeyOwner resolves whose provider key a skill/crew run's model calls spend
// (#2134), the way MintGatewayToken resolves it for a named box
// (resolveBoxKeyOwner): the box's cloud_org_id attribution label when the
// cloud stamped one -> org:<org_id>, else the run's owning username. A skill
// box is named agent-<skill> and shared by every caller of that skill, so the
// box name is not an owner; the run belongs to whoever dispatched it — the
// authenticated subject, the same identity the run's platform token records as
// its act claim (mintedAgentAct).
//
// Returns "" — no key_owner claim, the daemon-global key, as before #2134 —
// when there is no attributable owner (a system-started run with no subject;
// decided on FootprintAI/Containarium-cloud#1917, 2026-09-29) or when the
// resolved owner does not pass modelgateway.ValidateKeyOwner. A malformed
// owner is never stamped and never degrades to a different owner: a stamped
// but malformed attribution must not fall through to the caller's username,
// since that would bill someone the cloud did not attribute the box to.
func runKeyOwner(ctx context.Context, box *pb.Container, orgID string) string {
	var owner string
	if orgID != "" {
		// The request's own org attribution wins: the skill box is
		// shared across runs, so its label cannot say whose run this is.
		owner = modelgateway.OrgKeyOwner(orgID)
	} else if orgID := strings.TrimSpace(box.GetLabels()[cloudOrgIDLabel]); orgID != "" {
		owner = modelgateway.OrgKeyOwner(orgID)
	} else if username, _, ok := auth.SubjectFromGRPCContext(ctx); ok && username != "" {
		owner = modelgateway.UserKeyOwner(username)
	} else {
		return ""
	}
	if err := modelgateway.ValidateKeyOwner(owner); err != nil {
		log.Printf("[agent-skill] run key owner %q is not a valid key_owner (%v); minting with no key_owner claim", owner, err)
		return ""
	}
	return owner
}

// authorizeRunOrg validates RunAgentSkillRequest/RunCrewRequest.
// on_behalf_of_org_id and returns it, or "" when unset. Setting it
// spends that org's provider key, so only a service credential carrying the
// literal agents:run-for-org scope may; it must also be a well-formed id.
func authorizeRunOrg(ctx context.Context, orgID string) (string, error) {
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return "", nil
	}
	if !auth.HasLiteralScope(ctx, auth.ScopeAgentsRunForOrg) {
		return "", status.Errorf(codes.PermissionDenied,
			"on_behalf_of_org_id requires the %s scope", auth.ScopeAgentsRunForOrg)
	}
	if err := modelgateway.ValidateKeyOwner(modelgateway.OrgKeyOwner(orgID)); err != nil {
		return "", status.Errorf(codes.InvalidArgument, "on_behalf_of_org_id: %v", err)
	}
	return orgID, nil
}

// mintRunGatewayToken mints the gateway token for one skill/crew run on the box
// `name`, carrying the run's key_owner (runKeyOwner) and bound to provider —
// the run's RESOLVED provider (#2222), not necessarily the daemon's default.
// Every run path — a push run, a crew member, a queue worker — reaches it
// through provisionSkillBoxWith, which resolves provider once per run via
// agentengine.Resolve before calling here.
//
// An owner with no registered key is still minted a key_owner token: the
// gateway falls back to the daemon-global key for it and logs that call as
// billed to the operator (modelgateway resolveKey, case 3). That differs on
// purpose from MintGatewayToken, which refuses such a mint up front — a caller
// asking for a token can fix its config and retry, whereas a run that fails
// outright is worse than one that runs and is logged (#2134). This path never
// reaches here for a provider agentengine.Resolve already refused: that
// refusal happens in provisionSkillBoxWith before any mint is attempted.
//
// A run with no attributable owner mints no claim, and is logged here, once
// per run, rather than per model call.
//
// model (#2229) is the skill manifest's own pinned model — see
// gatewayProvisioning.mintGatewayToken's doc for what it does to the token.
func (s *AgentSkillServer) mintRunGatewayToken(ctx context.Context, name, skillID, runID, provider, model string, box *pb.Container, orgID string) (string, tokenid.MintedID, error) {
	keyOwner := runKeyOwner(ctx, box, orgID)
	if keyOwner == "" {
