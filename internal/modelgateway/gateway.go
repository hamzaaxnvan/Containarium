package modelgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/footprintai/containarium/internal/guardrail"
	"github.com/footprintai/containarium/internal/guardrailpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// UsageSink receives every metered model call so token usage can be forwarded
// to a durable/billing plane, in ADDITION to the in-memory Meter (which backs
// /__gateway/usage). The daemon wires an OTLP sink that emits per-tenant counters
// into the metrics pipeline (→ VictoriaMetrics → billing); standalone/test runs
// leave it nil. Kept an interface here so modelgateway stays free of an OTel
// dependency (the design's "metering plane writer", decoupled).
type UsageSink interface {
	RecordUsage(tenant, skill, provider string, u Usage)
}

// Config configures a Gateway.
type Config struct {
	Secret       []byte               // shared HMAC secret (the daemon's jwt.secret)
	Providers    map[string]*Provider // provider registry (see DefaultProviders)
	ProviderKeys map[string]string    // provider name -> REAL API key, held here only
	Sink         UsageSink            // optional: durable/billing usage writer (nil = in-memory only)
	Logger       *log.Logger
	// OutputFilter enables prompt-exfiltration redaction on the streaming chat
	// path (a hold-back window over the assistant text; #670 layer 2). Streaming
	// token metering is independent and always on. Fail-open regardless.
	OutputFilter bool

	// KeyResolver resolves the REAL upstream key per KEY OWNER for tokens that
	// carry a key_owner claim, so one daemon can broker many owners who each pay
	// for their own upstream (see keyowner.go). Nil — the pre-existing shape —
	// means every call resolves through ProviderKeys alone.
	//
	// ProviderKeys stays the daemon-global fallback either way: a token with no
	// key_owner never reaches the resolver.
	KeyResolver KeyResolver

	// DisableGlobalKeyFallback stops a token that carries a key_owner claim
	// from ever spending a ProviderKeys entry: an owner with no key of its own
	// is refused instead of billed to the operator (keyowner.go resolveKey,
	// case 3). Tokens WITHOUT a key_owner are unaffected. Off by default, which
	// is right for a self-hosted operator who runs everything on their own
	// global key; a hosted multi-tenant daemon turns it on so a stray
	// daemon-global key can never silently pay for tenants' inference.
	DisableGlobalKeyFallback bool

	// Revocations is the kill-switch for issued gateway tokens (see
	// revocation.go). Nil disables the check, which is the pre-existing
	// behavior — a standalone daemon with no Postgres has no revocation store
	// to consult. Production wires the same store used for platform JWTs.
	Revocations RevocationChecker

	// OwnerRevocations is the kill-switch for every token issued for ONE KEY
	// OWNER — "the customer removed their key" — as opposed to Revocations,
	// which kills one token by jti (see keyowner.go). Nil disables the check,
	// which is the pre-existing behavior.
	OwnerRevocations OwnerRevocationChecker

	// Policy configures per-tenant quota enforcement and the graduated response
	// ladder (see policy.go). Nil leaves the gateway metering-only: every tenant
	// stays in StateObserve and nothing is ever denied, which is the behavior
	// every existing deployment already has.
	Policy *PolicyConfig

	// AdminToken gates POST /__gateway/revoke (see admin.go). Empty (the
	// default) means the route is not registered at all — a request to it
	// 404s exactly like any other unregistered path, so an existing operator
	// who never sets this sees no change in the gateway's surface. Non-empty
	// requires "Authorization: Bearer <AdminToken>", compared in constant
	// time (#1820).
	AdminToken string

	// Inbound guardrail scan (#2367; see inbound.go). InboundPolicy is the
	// server policy source; nil (the standalone binary) means no policy
	// source exists, scanning is unavailable, and the gateway says so at
	// startup. With a provider wired, responses are scanned by InboundEngine
	// whenever the policy carries an inbound BLOCK rule, and every failure
	// to scan blocks. When the provider answers "not configured", responses
	// are instead scanned log-only against the baseline rules, never held
	// (inbound_baseline.go). InboundAudit receives one entry per block or
	// baseline detection (nil = log only). InboundHoldLimit (bytes; also
	// the baseline's scan buffer) and InboundScanTimeout take the Default*
	// constants when zero.
	InboundPolicy guardrailpolicy.PolicyProvider
	// InboundBaselineMode is the stored baseline mode (#2738), read with the
	// policy while none is configured. nil = always LOG. In BLOCK the
	// baseline takes the hold-and-block path instead of the tee.
	InboundBaselineMode guardrailpolicy.BaselineModeProvider
	InboundEngine       guardrail.Engine
	InboundAudit        InboundAuditSink
	InboundHoldLimit    int
	InboundScanTimeout  time.Duration
	// InboundPolicyTTL is how long a successful policy read is reused
	// (default DefaultInboundPolicyTTL; negative = read every call). Call
	// Gateway.InvalidateInboundPolicy when the policy changes so a new rule
	// does not wait for the TTL.
	InboundPolicyTTL time.Duration
	// InboundAuditQueue bounds entries waiting for InboundAudit (default
	// DefaultInboundAuditQueue; a full queue drops and counts) and
	// InboundAuditTimeout bounds one sink write (default
	// DefaultInboundAuditTimeout). Sink writes never run on the request path.
	InboundAuditQueue   int
	InboundAuditTimeout time.Duration

	// Calls receives one typed event per proxied model call, keyed by the
	// token's run id, so a failure at the gateway can be shown with the run
	// (#2928; see calls.go). Nil records nothing.
	Calls CallRecorder
}

// Gateway brokers every agent box's model calls: it authenticates the box's
// scoped gateway token, injects the real provider key (which never leaves the
// gateway), proxies to the provider, and meters per-tenant token usage.
type Gateway struct {
	cfg     Config
	meter   *Meter
	policy  *Policy
	inbound *inbound

	// Request-lifecycle observability: a monotonic request id, a live
	// in-flight gauge, and lifetime completed/failed counters. These make
	// "did the gateway finish this request or is it still running/hung?"
	// answerable — from the START/END log pair per request and the live
	// counts on /__gateway/status.
	reqSeq    atomic.Uint64
	inflight  atomic.Int64
	completed atomic.Uint64
	failed    atomic.Uint64
}

// New builds a Gateway.
func New(cfg Config) *Gateway {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	pc := PolicyConfig{}
	if cfg.Policy != nil {
		pc = *cfg.Policy
	}
	return &Gateway{cfg: cfg, meter: NewMeter(), policy: NewPolicy(pc), inbound: newInbound(cfg)}
}

// InboundStatus exposes the inbound scan state: whether a policy source is
// wired, whether the first read has happened, whether the last known policy
// scans, and the block counters.
func (g *Gateway) InboundStatus() InboundStatus { return g.inbound.status() }

// FlushInboundAudit waits until every inbound-block audit entry queued so far
// has been handed to the sink, or ctx ends. Call it before shutting the daemon
// down; the request path never does.
func (g *Gateway) FlushInboundAudit(ctx context.Context) error { return g.inbound.flushAudit(ctx) }

// InvalidateInboundPolicy makes the next model call re-read the inbound
// policy instead of waiting out the cache TTL.
func (g *Gateway) InvalidateInboundPolicy() { g.inbound.invalidate() }

// Meter exposes the usage rollups (for tests / the usage endpoint).
func (g *Gateway) Meter() *Meter { return g.meter }

// Policy exposes the enforcement ladder so the daemon can drive the operator
// verbs (revoke / clear) and read tenant state.
func (g *Gateway) Policy() *Policy { return g.policy }

const modelPrefix = "/v1/model/"

// Handler returns the gateway's HTTP mux: the model data plane plus a usage
// readout and a health check.
func (g *Gateway) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(modelPrefix, g.handleModel)
	mux.HandleFunc("/__gateway/usage", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(g.meter.Snapshot())
	})
	// Per-tenant enforcement state: which rung each tenant is on, what put it
	// there, and its consumption against budget. The readout an operator needs
	// before deciding whether a throttled tenant is a runaway or a false
	// positive.
	mux.HandleFunc("/__gateway/policy", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(g.policy.Status())
	})
	mux.HandleFunc("/__gateway/healthz", func(w http.ResponseWriter, r *http.Request) {
		// Cold start with a policy provider wired: until the first policy
		// read succeeds the gateway cannot know whether to scan, so it is
		// not ready rather than guessing in either direction.
		if !g.inbound.ready(r.Context()) {
			http.Error(w, "inbound guardrail policy not yet read", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// Admin kill-switch (#1820). Registered ONLY when an operator opts in by
	// setting AdminToken — an unregistered path 404s on ServeMux's own
	// default, which is how a standalone gateway with no admin token stays
	// byte-for-byte unchanged from before this route existed. See admin.go.
	if g.cfg.AdminToken != "" {
		mux.HandleFunc("/__gateway/revoke", g.handleAdminRevoke)
	}
	// Live request-lifecycle gauge: how many model calls are in flight right
	// now, and how many have completed/failed over the gateway's lifetime.
	// `inflight` stuck above zero with no new completions is the "hung request"
	// signal the per-request START/END logs let you drill into.
	mux.HandleFunc("/__gateway/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"inflight":  g.inflight.Load(),
			"completed": g.completed.Load(),
			"failed":    g.failed.Load(),
			"inbound":   g.inbound.status(),
		})
	})
	return mux
}

// bearer pulls the gateway token from the request, accepting the three shapes
// the provider SDKs use when pointed at a proxy base URL: Authorization Bearer
// (Anthropic ANTHROPIC_AUTH_TOKEN / OpenAI), x-api-key (Anthropic raw), and
// x-goog-api-key (Gemini).
func bearer(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if v := r.Header.Get("x-api-key"); v != "" {
		return v
	}
	if v := r.Header.Get("x-goog-api-key"); v != "" {
		return v
	}
	return ""
}

func (g *Gateway) handleModel(w http.ResponseWriter, r *http.Request) {
	// path: /v1/model/<provider>/<upstream path...>
	rest := strings.TrimPrefix(r.URL.Path, modelPrefix)
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		http.Error(w, "missing provider in path", http.StatusNotFound)
		return
	}
	provName := rest[:slash]
	upstreamPath := rest[slash:] // includes leading '/'
	prov := g.cfg.Providers[provName]
	if prov == nil {
		http.Error(w, "unknown provider: "+provName, http.StatusNotFound)
		return
	}

	tok := bearer(r)
	if tok == "" {
		http.Error(w, "missing gateway token", http.StatusUnauthorized)
		return
	}
	claims, err := VerifyToken(g.cfg.Secret, tok)
	if err != nil {
		http.Error(w, "invalid gateway token: "+err.Error(), http.StatusUnauthorized)
		return
	}
	if claims.Provider != provName {
		// A token presented against a provider it isn't scoped for is a probe,
		// not a misconfiguration — the box is seeded with exactly one base URL.
		// Recorded so the auth-failure-ratio signal can see it.
		g.policy.RecordDenial(claims.Tenant)
		http.Error(w, "token not valid for provider "+provName, http.StatusForbidden)
		return
	}

	// Kill-switch. Deliberately ahead of the key lookup and the Director, so a
	// revoked token never causes the real provider key to be touched.
	if g.isRevoked(r.Context(), claims.ID) {
		g.cfg.Logger.Printf("model-gateway: REVOKED tenant=%s skill=%s provider=%s jti=%s",
			claims.Tenant, claims.SkillID, provName, claims.ID)
		http.Error(w, "gateway token revoked", http.StatusUnauthorized)
		return
	}

	// Owner kill-switch: every token issued for this key owner before the
	// recorded cutoff is dead (the "customer removed their key" case). Sits
	// beside the per-jti check and ahead of the key lookup for the same reason
	// — a revoked token must never cause a real provider key to be touched.
	if g.isOwnerRevoked(r.Context(), claims) {
		g.cfg.Logger.Printf("model-gateway: REVOKED-OWNER tenant=%s key_owner=%s provider=%s jti=%s",
			claims.Tenant, claims.KeyOwner, provName, claims.ID)
		http.Error(w, OwnerRevokedMessage, http.StatusUnauthorized)
		return
	}

	// Whose key this call spends: the token's key_owner if it has one and a
	// resolver can answer for it, else the daemon-global key (keyowner.go).
	key, ok, keyOwner := g.resolveKeyOwner(r.Context(), claims, provName)
	if !ok {
		g.recordCall(claims, newCall(claims, provName, "", pb.GatewayKeyOwner_GATEWAY_KEY_OWNER_UNSPECIFIED, int32(http.StatusBadGateway),
			pb.GatewayTransportError_GATEWAY_TRANSPORT_ERROR_NO_KEY, time.Now(), time.Now()))
		http.Error(w, "gateway holds no key for provider "+provName, http.StatusBadGateway)
		return
	}

	// pathModel: for Gemini the model is in the URL path. Other providers
	// carry it in the request body (reqModel, extracted below) — pathModel
	// stays "" for them and is only ever a fallback for logModel.
	pathModel := ""
	if provName == "gemini" {
		pathModel = geminiModelFromPath(upstreamPath)
	}

	upstream, err := url.Parse(prov.UpstreamURL)
	if err != nil {
		http.Error(w, "bad upstream url", http.StatusInternalServerError)
		return
	}

	// For OpenAI-shaped providers, buffer the request body so we can (a) extract
	// the system prompt (skill persona) for streaming output-filtering and (b)
	// enable a final usage event so the SSE path is metered. Fail-open: any read
	// error leaves the original body in place.
	sysPrompt, reqModel := "", ""
	// Anthropic carries the model in the request body and nowhere else — not in
	// the path like Gemini, and it isn't OpenAI-shaped so it misses the branch
	// below. Without this the model-switch signal is silent for the provider
	// that skill boxes are provisioned for by default, and the lifecycle logs
	// report an empty model for it. Read-only: no body rewriting, since
	// ensureStreamUsage and extractSystemPrompt are OpenAI shapes.
	if provName == "anthropic" {
		if raw, rerr := io.ReadAll(r.Body); rerr == nil {
			_ = r.Body.Close()
			reqModel = requestModel(raw)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
			r.Header.Set("Content-Length", strconv.Itoa(len(raw)))
