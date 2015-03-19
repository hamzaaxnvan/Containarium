package modelgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/footprintai/containarium/internal/guardrail"
	"github.com/footprintai/containarium/internal/guardrailpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Inbound guardrail enforcement (#2367 slice B2;
// docs/architecture/guardrail-inbound-and-server-policy.md, "Enforcement in
// the gateway"). A model response is scanned when the server policy has at
// least one BLOCK rule for an inbound kind; otherwise the gateway behaves
// exactly as it did before this file existed. In scanned mode the whole
// response is held inside ModifyResponse, scanned once, and then either
// replayed unchanged or replaced by a typed error. Every failure to scan
// (engine down, unsupported kind, coverage gap, compressed or unrecognised
// body, held message over the byte limit) blocks: there is no fail-open
// switch, and the break-glass is an admin clearing the inbound rules.

const (
	// DefaultInboundHoldLimit bounds the held response. Past it the buffer
	// is discarded and the response blocked, so one long or hostile
	// response cannot pin memory per request.
	DefaultInboundHoldLimit = 8 << 20
	// DefaultInboundScanTimeout bounds one engine round trip.
	DefaultInboundScanTimeout = 15 * time.Second
	// DefaultInboundPolicyTTL is how long a successful policy read is reused
	// (#2454). A negative Config.InboundPolicyTTL reads on every call.
	DefaultInboundPolicyTTL = 5 * time.Second
	// DefaultInboundAuditQueue bounds the audit entries waiting for the sink.
	// A full queue drops the newest entry and counts it: audit delivery is
	// best-effort by design, because a block is never conditional on it.
	DefaultInboundAuditQueue = 256
	// DefaultInboundAuditTimeout bounds one audit write. It is separate from
	// (and much shorter than) the engine scan timeout: the write happens off
	// the request path, but a wedged sink must not stall the worker forever.
	DefaultInboundAuditTimeout = 3 * time.Second
	// InboundBlockType is the "type" of the error body a blocked response
	// receives, so a client can tell a guardrail block from an upstream error.
	InboundBlockType = "guardrail_inbound_block"
	// BaselineEngineID is the engine id a baseline BLOCK-mode block records
	// (#2738): the baseline rule table, not the policy engine.
	BaselineEngineID = "baseline"
)

// InboundAuditSink receives one entry per blocked response, and one per
// baseline detection (RecordInboundDetection, #2736). Kept an
// interface so the gateway has no dependency on internal/audit; the daemon
// adapts it onto audit.Store.Log. A failing sink never un-blocks a response,
// and it is never called on the request path: entries go through a bounded
// queue to one worker (see inbound.enqueueAudit), so a slow or down store
// cannot delay a block or a refusal.
type InboundAuditSink interface {
	RecordInboundBlock(ctx context.Context, b InboundBlock) error
	RecordInboundDetection(ctx context.Context, d InboundDetection) error
}

// InboundDetection is the audit record of one baseline rule that matched in
// one response while no admin policy exists (#2736): attribution, the rule
// id, kind and action, and the match count. Never the matched text.
type InboundDetection struct {
	Tenant, SkillID, Provider, Model string
	RuleID                           guardrail.BaselineRuleID
	Kind                             pb.GuardrailKind
	Action                           guardrail.BaselineAction
	Matches                          int
}

// InboundBlock is the audit record of one block: attribution, the typed
// reason, kinds and counts. Never the flagged text, never the response body.
type InboundBlock struct {
	Tenant, SkillID, Provider, Model string
	Reason                           guardrail.InboundReason
	Kinds                            []pb.GuardrailKind
	Findings                         int
	EngineID                         string
	PolicyRevision                   int64
}

// InboundBlockBody is the error body a blocked response carries.
type InboundBlockBody struct {
	Error InboundBlockError `json:"error"`
}

// InboundBlockError names the block; kinds are the enum names without the
// GUARDRAIL_KIND_ prefix. No field carries response text.
type InboundBlockError struct {
	Type    string   `json:"type"`
	Reason  string   `json:"reason"`
	Kinds   []string `json:"kinds,omitempty"`
	Message string   `json:"message"`
}

// InboundStatus is the readout on /__gateway/status and the input a
// `code run` derives its scan status from.
type InboundStatus struct {
	// PolicySource is whether a policy provider is wired at all. False is
	// the standalone gateway: scanning is unavailable, visibly.
	PolicySource bool `json:"policy_source"`
	// Ready is false only while a wired provider has never answered (cold
	// start); the gateway refuses model calls until it has.
	Ready bool `json:"ready"`
	// Scanning is whether the last known policy has an inbound BLOCK rule.
	Scanning       bool  `json:"scanning"`
	PolicyRevision int64 `json:"policy_revision"`
	// AuditQueued is the audit entries waiting for the sink; AuditDropped
	// those discarded because the queue was full; AuditFailed those the sink
	// rejected or timed out on. A block is never conditional on any of them.
	AuditQueued  int    `json:"audit_queued"`
	AuditDropped uint64 `json:"audit_dropped"`
	AuditFailed  uint64 `json:"audit_failed"`
	// UpstreamErrors counts non-2xx responses with an unrecognised content
	// type (a rate limit, a load-balancer page) passed through unchanged.
	// They are not model output and are not counted as blocks.
