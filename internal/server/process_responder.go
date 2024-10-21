package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/threatdetect"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Process responder (#2636): turns a HIGH mining finding into the effective
// ProcessPolicy's action — ALERT (default), THROTTLE or FREEZE — records it
// in security_actions, and reverses it on operator release or when the TTL
// backstop fires. See docs/architecture/process-exec-guardrail.md,
// component 7.

// actionAuditResourceType is the audit_logs.resource_type for every apply,
// release, expiry and failure — category security.action per the design.
const actionAuditResourceType = "security.action"

// processActionSweepInterval is how often Run looks for expired actions.
const processActionSweepInterval = 60 * time.Second

// processResponseQueue is the depth of the responder's work queue (#2638
// review). The async ActingSink hands each upsert to it so the responder's
// Incus calls (the kill's init-pid read, throttle, freeze) never run on the
// goroutine that wrote the finding — for a signature finding that is the
// exec ring consumer. A full queue drops the job (counted, logged, and
// audited by the worker) rather than blocking that goroutine.
const processResponseQueue = 256

// processActionRetryBackoff is how long after a FAILED apply the responder
// leaves the container alone instead of retrying on every rule re-fire.
const processActionRetryBackoff = 10 * time.Minute

// containerActuator is the slice of the Incus client the responder needs.
// An interface, not *incus.Client, so the responder is unit-testable with
// a fake; incusActuator adapts the real client.
type containerActuator interface {
	// CPULimit reads the box's current limits.cpu and limits.cpu.allowance
	// ("" when unset).
	CPULimit(name string) (count, allowance string, err error)
	// SetCPULimit writes both keys verbatim ("" unsets).
	SetCPULimit(name, count, allowance string) error
	Freeze(name string) error
	Unfreeze(name string) error
	// InstanceID reads the box's Incus instance id (volatile.uuid, #2835):
	// a box re-created under the same name has another one. An error that
	// incus.IsNotFound accepts means there is no such box.
	InstanceID(name string) (string, error)
}

// incusActuator adapts *incus.Client to containerActuator.
type incusActuator struct{ c *incus.Client }

func (a incusActuator) CPULimit(name string) (string, string, error) {
	cfg, _, err := a.c.GetRawInstance(name)
	if err != nil {
		return "", "", err
	}
	return cfg["limits.cpu"], cfg["limits.cpu.allowance"], nil
}

func (a incusActuator) SetCPULimit(name, count, allowance string) error {
	return a.c.SetCPUConfig(name, count, allowance)
}

func (a incusActuator) InstanceID(name string) (string, error) {
	cfg, _, err := a.c.GetRawInstance(name)
	if err != nil {
		return "", err
	}
	return cfg[incus.InstanceIDKey], nil
}

func (a incusActuator) Freeze(name string) error   { return a.c.FreezeContainer(name) }
func (a incusActuator) Unfreeze(name string) error { return a.c.UnfreezeContainer(name) }

// findingLifecycle is what the responder needs from a finding store:
// *threatdetect.FindingStore and *threatdetect.MemFindingStore both satisfy
// it.
type findingLifecycle interface {
	Upsert(ctx context.Context, f *threatdetect.Finding) (*threatdetect.Finding, error)
	Get(ctx context.Context, id int64) (*threatdetect.Finding, error)
	Resolve(ctx context.Context, id int64) (*threatdetect.Finding, error)
	SetResponse(ctx context.Context, id int64, r threatdetect.FindingResponse) (*threatdetect.Finding, error)
	SetResponseHeld(ctx context.Context, id int64, box, standing threatdetect.FindingResponse) (*threatdetect.Finding, error)
	ResolveWithResponse(ctx context.Context, id int64, r threatdetect.FindingResponse) (*threatdetect.Finding, error)
}

// ProcessResponder applies and releases process actions. One instance per
// daemon; apply/release are serialized so the open-row check and the Incus
// call cannot interleave for the same container. Audit entries go through
// the package's auditLogger (agent_server.go), satisfied by *audit.Store.
type ProcessResponder struct {
	policies *threatdetect.ProcessPolicyStore
	actions  threatdetect.ActionStore
	findings findingLifecycle
	act      containerActuator
	audit    auditLogger
	// degraded: the finding store has no Postgres, so a throttle's
	// previous-limit row could vanish on restart. The responder then applies
	// ALERT whatever the policy says (decided on #2641 Q2).
	degraded bool
	now      func() time.Time
	// killer SIGKILLs a matched process for kill_process (#2638); nil
	// disables the kill whatever the policy says.
	killer processKiller

	// queue feeds the worker started by Run (async ActingSink only).
	queue   chan responseJob
	dropped atomic.Uint64
	// droppedAudited is the dropped count the worker last wrote an audit
	// row for; touched only by the worker goroutine.
	droppedAudited uint64

	mu        sync.Mutex
	backendID string

	// bg tracks the background closes ContainerDeleted starts; tests wait on
	// it. deletedCloseTimeout bounds each one's store and Incus work.
	bg                  sync.WaitGroup
	deletedCloseTimeout time.Duration
	// registerDelete hooks Incus deletes (incus.OnContainerDeleted); Run
	// registers ContainerDeleted with it and removes it when Run ends.
	registerDelete func(fn func(name, instanceID string)) (remove func())
}

// deletedCloseTimeout is how long closing a deleted box's action may take
// before it gives up and leaves it to the sweeps (#2835 review).
const deletedCloseTimeout = 10 * time.Second

// NewProcessResponder wires the responder. degraded forces ALERT-only.
func NewProcessResponder(policies *threatdetect.ProcessPolicyStore, actions threatdetect.ActionStore, findings findingLifecycle, act containerActuator, auditLog auditLogger, degraded bool) *ProcessResponder {
	return &ProcessResponder{
		policies: policies, actions: actions, findings: findings, act: act, audit: auditLog,
		degraded: degraded, now: time.Now,
		queue: make(chan responseJob, processResponseQueue),

		deletedCloseTimeout: deletedCloseTimeout,
		registerDelete:      incus.OnContainerDeleted,
	}
}

// respondable reports whether the responder could act on f at all: an open
// HIGH finding from a mining rule, attributed to a container.
func respondable(f *threatdetect.Finding) bool {
	return f != nil && f.Container != "" && f.State == threatdetect.FindingStateOpen &&
		f.Severity == pb.ThreatSeverity_THREAT_SEVERITY_HIGH && threatdetect.RespondsTo(f.Rule)
}

// responseJob is one finding handed to the worker, with the exec a kill
// would target.
type responseJob struct {
	f      *threatdetect.Finding
	target *threatdetect.ExecEvidence
	// at is when the job was queued: a release stamped at or after it means
	// an operator (or the TTL backstop) decided after this response was
	// asked for, and the job must not undo that decision. Truncated to the
	// microsecond, the precision Postgres stores released_at at, so a
	// release in the same microsecond still counts as at-or-after.
	at time.Time
}

// enqueue offers a job to the worker without ever blocking the caller.
func (r *ProcessResponder) enqueue(f *threatdetect.Finding, target *threatdetect.ExecEvidence) bool {
	cp := *f
	job := responseJob{f: &cp, at: r.now().Truncate(time.Microsecond)}
	if target != nil {
		t := *target
		job.target = &t
	}
	select {
	case r.queue <- job:
		return true
	default:
		if n := r.dropped.Add(1); n == 1 || n%100 == 0 {
			log.Printf("[process-responder] work queue full (cap %d): dropped response for finding %d on %s (%d dropped so far)",
				processResponseQueue, f.ID, f.Container, n)
		}
		return false
	}
}

// DroppedResponses counts findings the responder never acted on because its
// queue was full.
func (r *ProcessResponder) DroppedResponses() uint64 { return r.dropped.Load() }

// runQueue is the worker: it acts on queued findings one at a time, so the
// Incus work happens here and never on the writer's goroutine.
func (r *ProcessResponder) runQueue(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-r.queue:
			r.runJob(ctx, job)
			r.auditDrops(ctx)
		}
	}
}

// runJob acts on one queued job, but only if it is still current. The queued
// copy of the finding was taken when the rule fired; by the time the worker
// gets to it an operator may have released the box's action, the sentry or
// an operator may have resolved the finding, or the TTL backstop may have
// released the action. Acting on the stale copy would re-throttle a box the
// operator just released, write APPLIED onto a resolved finding, or kill a
// process whose finding an operator just closed. So the finding and the
// box's own action row are read under r.mu right before the kill (readJob,
// one read for both) and the result is what the job then acts on; they are
// read once more under r.mu after the kill, because the kill runs without
// the lock. A stale job is skipped and audited as "response-skipped". A
// release counts only on the job's own container and only for this
// finding: a box can carry a release for a different finding, and legacy
// shared findings can still have stored actions on several boxes. A store read
// that fails is not a reason to skip: the job is not stale as far as anyone
// can tell, so it goes on (the locked read retries once).
func (r *ProcessResponder) runJob(ctx context.Context, job responseJob) {
	r.onFindingChecked(ctx, job.f, job.target, func() (*threatdetect.Finding, string) {
		v := r.readJob(ctx, job)
		if v.err != nil {
			log.Printf("[process-responder] finding %d on %s: read for queued response: %v; reading again", job.f.ID, job.f.Container, v.err)
			v = r.readJob(ctx, job)
		}
		reason := v.stale(job)
		switch {
		case reason != "":
			r.auditSkip(ctx, job, reason)
		case v.err != nil:
			log.Printf("[process-responder] finding %d on %s: read for queued response failed twice (%v); acting on the queued copy", job.f.ID, job.f.Container, v.err)
		}
		return v.current(job), reason
	})
}

// jobView is one read of what a queued job depends on: the finding as the
// store has it now, and the job's own box's latest action row for that
// finding (a box's row for another finding says nothing about this one).
// A half whose read failed is nil with the error in err: the view is then
// inconclusive, not stale, and the caller decides whether to read again or
// go on with the queued copy.
type jobView struct {
	finding *threatdetect.Finding
	// gone: the store has no such finding any more (definite).
	gone bool
	last *threatdetect.SecurityAction
	err  error
}

// readJob reads a jobView. The finding keeps the job's container: the
// legacy row may carry the first container that opened it, and the action
// belongs to the box whose exec fired.
func (r *ProcessResponder) readJob(ctx context.Context, job responseJob) jobView {
	var v jobView
	f, err := r.findings.Get(ctx, job.f.ID)
	switch {
	case errors.Is(err, threatdetect.ErrFindingNotFound):
		v.gone = true
	case err != nil:
		v.err = fmt.Errorf("finding: %w", err)
	default:
		cp := *f
		cp.Container = job.f.Container
		v.finding = &cp
	}
	rows, err := r.actions.ListByFinding(ctx, job.f.ID)
	if err != nil {
		v.err = errors.Join(v.err, fmt.Errorf("actions: %w", err))
		return v
	}
	for _, row := range rows { // newest first
		if row.Container == job.f.Container {
			v.last = row
			break
		}
	}
	return v
}

// stale is the definite reason the job must not run: the finding is gone
// or no longer open, or the job's own container had its action for this
// finding released at or after the job was queued. A failed apply (released
// by "error") is not a decision and does not count. "" when the job may
// run, including when the view is inconclusive.
func (v jobView) stale(job responseJob) string {
	switch {
	case v.gone:
		return "finding no longer exists"
	case v.finding != nil && v.finding.State != threatdetect.FindingStateOpen:
		return "finding no longer open"
	case v.last != nil && !v.last.ReleasedAt.IsZero() && v.last.ReleasedBy != threatdetect.ReleasedByError && !v.last.ReleasedAt.Before(job.at):
		return "action on " + job.f.Container + " released by " + v.last.ReleasedBy + " after the response was queued"
	}
	return ""
}

// current is the finding the job acts on: the fresh read, or the queued
// copy when the finding could not be read.
func (v jobView) current(job responseJob) *threatdetect.Finding {
	if v.finding != nil {
		return v.finding
	}
	cp := *job.f
	return &cp
}

func (r *ProcessResponder) auditSkip(ctx context.Context, job responseJob, reason string) {
	r.auditLog(ctx, "response-skipped", job.f.Container, fmt.Sprintf("finding=%d rule=%s tenant=%s container=%s queued_at=%s reason=%q",
		job.f.ID, job.f.Rule, job.f.TenantID, job.f.Container, job.at.UTC().Format(time.RFC3339Nano), reason))
	log.Printf("[process-responder] queued response for finding %d on %s skipped: %s", job.f.ID, job.f.Container, reason)
}

// auditDrops writes one audit row per batch of newly dropped responses, so a
// drop is visible in the trail and not only in the log. Worker goroutine only.
func (r *ProcessResponder) auditDrops(ctx context.Context) {
	n := r.dropped.Load()
	if n == r.droppedAudited {
		return
	}
	r.auditLog(ctx, "response-dropped", "queue", fmt.Sprintf("dropped=%d total_dropped=%d queue_cap=%d",
		n-r.droppedAudited, n, processResponseQueue))
	r.droppedAudited = n
}

// SetProcessKiller wires the host-side kill used by kill_process (#2638).
// Call before the responder sees findings.
func (r *ProcessResponder) SetProcessKiller(k processKiller) { r.killer = k }

// Degraded reports whether responses are disabled for lack of persistence
// (GetSentryStatus surfaces it as "responses disabled").
func (r *ProcessResponder) Degraded() bool { return r.degraded }

// SetBackendID stamps the backend id onto the PROCESS_ACTION_EXPIRED
// findings the TTL sweep raises; same late-binding as Engine.SetBackendID.
func (r *ProcessResponder) SetBackendID(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backendID = id
}

// OnFinding is called after every successful finding upsert (ActingSink).
// It acts only on an open HIGH finding from a mining rule with no action
// already open on its container, and returns the finding with its response
// recorded (nil when nothing changed).
func (r *ProcessResponder) OnFinding(ctx context.Context, f *threatdetect.Finding) *threatdetect.Finding {
	return r.onFinding(ctx, f, lastExec(f))
}

// lastExec is the newest exec on f's evidence, nil when it carries none.
func lastExec(f *threatdetect.Finding) *threatdetect.ExecEvidence {
	if f == nil || len(f.Evidence.Execs) == 0 {
		return nil
	}
	return &f.Evidence.Execs[len(f.Evidence.Execs)-1]
}

// onFinding is OnFinding with the exec a kill targets given explicitly: the
// ActingSink passes the exec of the write that triggered this call, which is
// the process that just matched, independent of how the store merged or
// capped the stored evidence.
func (r *ProcessResponder) onFinding(ctx context.Context, f *threatdetect.Finding, target *threatdetect.ExecEvidence) *threatdetect.Finding {
	return r.onFindingChecked(ctx, f, target, nil)
}

// onFindingChecked is onFinding with an optional recheck run under r.mu
// before anything that touches the box — the kill as much as the policy
// action. recheck returns the finding as the store has it now (what the
// rest of the response acts on) and a non-empty reason when the job is
// stale, which aborts (the queued-job path, see runJob).
func (r *ProcessResponder) onFindingChecked(ctx context.Context, f *threatdetect.Finding, target *threatdetect.ExecEvidence, recheck func() (*threatdetect.Finding, string)) *threatdetect.Finding {
	if !respondable(f) {
		return nil
	}
	// kill_process (#2638) is additive and per process: it runs for every
	// matched exec, before and independent of the one-action-per-container
	// policy action below, which still applies. It needs no responder state,
	// so it runs outside the lock: its Incus read never holds up a release.
	// But a queued job is rechecked under r.mu first (#2638 review): a
	// Release or resolve that landed while the job waited takes r.mu, so once
	// the recheck passes under it no release is in flight, and a finding an
	// operator already closed is never the reason a process dies.
	if recheck != nil {
		cur, stale := r.stale(recheck)
		if stale {
			return nil
		}
		f = cur
	}
	// One read of the effective policy serves the kill and the policy
	// action (#2768), and only if either needs it.
	policyOf := r.effectivePolicyOnce(ctx, f.Container)
	ko := r.maybeKill(ctx, f, target, policyOf)
	killedPID := ko.pid

	r.mu.Lock()
	defer r.mu.Unlock()
	if recheck != nil {
		cur, reason := recheck()
		if reason != "" {
			if killedPID == 0 {
				return nil
			}
			// Released or resolved during the kill itself (after the recheck
			// above let go of r.mu). The policy action is off, but the
			// process is dead and the trail must say so: set process_killed
			// on whatever response the release wrote, not on the pre-kill
			// copy.
			return r.recordKill(ctx, cur, killFacts(cur.Response, ko))
		}
		f = cur
	}
	// The kill facts fold this attempt into the response the recheck just
	// read under r.mu (or, without a recheck, the one the upsert returned), so
	// kills on the same finding that were queued separately add up.
	kill := killFacts(f.Response, ko)

	if _, err := r.actions.Open(ctx, f.Container); err == nil {
		// One action per container; the open one stands.
		return r.recordKill(ctx, f, kill)
	} else if !errors.Is(err, threatdetect.ErrNoOpenAction) {
		log.Printf("[process-responder] finding %d on %s: read open action: %v", f.ID, f.Container, err)
		return r.recordKill(ctx, f, kill)
	}
	if last, err := r.actions.Latest(ctx, f.Container); err == nil &&
		last.ReleasedBy == threatdetect.ReleasedByError && r.now().Sub(last.AppliedAt) < processActionRetryBackoff {
		// The last apply failed moments ago: re-firing would only write
		// another error row. Say so once, on the first suppressed re-fire.
		if f.Response == nil || f.Response.State != pb.ResponseState_RESPONSE_STATE_FAILED {
			log.Printf("[process-responder] finding %d on %s: last %s failed at %s; not retrying for %s",
				f.ID, f.Container, last.Action, last.AppliedAt.UTC().Format(time.RFC3339), processActionRetryBackoff)
		}
		return r.recordKill(ctx, f, kill)
	}
	policy, err := policyOf()
	if err != nil {
		// Unknown policy: no policy action rather than guess. The finding
		// itself is already recorded and delivered; a kill that already ran
		// is still recorded.
		log.Printf("[process-responder] finding %d on %s: read policy: %v", f.ID, f.Container, err)
		return r.recordKill(ctx, f, kill)
	}
	action := policy.GetAction()
	if r.degraded {
		action = pb.ProcessAction_PROCESS_ACTION_ALERT
	}
	now := r.now()
	detail := fmt.Sprintf("finding=%d rule=%s tenant=%s container=%s policy=%s", f.ID, f.Rule, f.TenantID, f.Container, policy.GetAction())

	switch action {
	case pb.ProcessAction_PROCESS_ACTION_THROTTLE, pb.ProcessAction_PROCESS_ACTION_FREEZE:
	default:
		if killedPID == 0 && f.Response != nil && kill.skip == f.Response.KillSkipReason && (f.Response.Action == pb.ProcessAction_PROCESS_ACTION_ALERT && f.Response.State == pb.ResponseState_RESPONSE_STATE_NONE ||
			f.Response.State == pb.ResponseState_RESPONSE_STATE_APPLIED && f.Response.Container != f.Container) {
			// A repeat of a finding already recorded as alert-only, or an
			// alert-only box of a shared finding another box's action still
			// stands on (#2820): nothing new to record.
			return nil
		}
		if r.degraded {
			detail += " degraded=true"
		}
		out := r.writeBox(ctx, f.ID, kill.on(threatdetect.FindingResponse{
			Action: pb.ProcessAction_PROCESS_ACTION_ALERT, State: pb.ResponseState_RESPONSE_STATE_NONE, AppliedAt: now, Container: f.Container,
		}))
		r.auditLog(ctx, "alert", f.Container, detail)
		return out
	}

	row := &threatdetect.SecurityAction{
		FindingID: f.ID, Container: f.Container, TenantID: f.TenantID, Action: action,
		AppliedAt: now, ExpiresAt: now.Add(policy.GetTtl().AsDuration()), KilledPID: killedPID,
	}
	// Record first, act second: the row (with the previous limits a
	// THROTTLE restores) exists before the box is touched, so a crash
	// between the two leaves a PENDING row the sweep can undo.
	if action == pb.ProcessAction_PROCESS_ACTION_THROTTLE {
		count, allow, err := r.act.CPULimit(row.Container)
		if err != nil {
			return r.recordFailure(ctx, f, row, fmt.Errorf("read cpu limit: %w", err), detail, kill)
		}
		row.PrevCPUCount, row.PrevCPUAllowance = count, allow
	}
	// The instance the action is applied to (#2835), so no later undo can
	// write its saved limits onto another box re-created under the name.
	// Unknown is recorded as "": the undo then runs as it always did.
	if id, err := r.act.InstanceID(row.Container); err == nil {
		row.InstanceID = id
	} else {
		log.Printf("[process-responder] finding %d on %s: read instance id: %v", f.ID, f.Container, err)
	}
	row.Pending = true
	if err := r.actions.Insert(ctx, row); err != nil {
		return r.recordFailure(ctx, f, nil, fmt.Errorf("record action: %w", err), detail, kill)
	}
	if err := r.apply(row, policy.GetThrottleAllowance()); err != nil {
		return r.recordFailure(ctx, f, row, err, detail, kill)
	}
	if err := r.actions.MarkApplied(ctx, row.ID); err != nil {
		log.Printf("[process-responder] confirm %s on %s (action %d): %v", row.Action, row.Container, row.ID, err)
	}
	row.Pending = false
	out := r.setResponse(ctx, f.ID, kill.on(threatdetect.FindingResponse{
		Action: action, State: pb.ResponseState_RESPONSE_STATE_APPLIED, AppliedAt: now, ExpiresAt: row.ExpiresAt, Container: f.Container,
	}))
	r.auditLog(ctx, "apply", fmt.Sprintf("%d", row.ID), fmt.Sprintf("%s action=%s prev_cpu=%q prev_allowance=%q allowance=%q expires_at=%s",
		detail, action, row.PrevCPUCount, row.PrevCPUAllowance, policy.GetThrottleAllowance(), row.ExpiresAt.UTC().Format(time.RFC3339)))
	log.Printf("[process-responder] %s applied to %s (finding %d, expires %s)", action, f.Container, f.ID, row.ExpiresAt.UTC().Format(time.RFC3339))
	return out
}

// stale runs recheck under r.mu and reports whether the job it guards is no
// longer current, with the finding as read. Takes and releases the lock
// itself so the caller's next step (the kill's Incus read) runs without it.
func (r *ProcessResponder) stale(recheck func() (*threatdetect.Finding, string)) (*threatdetect.Finding, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, reason := recheck()
	return cur, reason != ""
}

// killOutcome is what maybeKill did: the killed pid, or why an attempted kill
// was skipped. Both are delivered on the finding's response (#2775).
type killOutcome struct {
	pid  uint32
	skip pb.KillSkipReason
}

// killNote is the kill facts a response write carries: whether a process was
// killed (this attempt or an earlier one), the latest pid and the count of
// kills on the finding, and why the last attempted kill was skipped.
type killNote struct {
	killed bool
	pid    uint32
	count  uint32
	skip   pb.KillSkipReason
}

// into sets the kill facts on a response about to be written.
func (n killNote) into(r *threatdetect.FindingResponse) {
	r.ProcessKilled, r.KilledPID, r.Kills, r.KillSkipReason = n.killed, n.pid, n.count, n.skip
}

// on returns resp with the kill facts set: the one way every response write
// carries them.
func (n killNote) on(resp threatdetect.FindingResponse) threatdetect.FindingResponse {
	n.into(&resp)
	return resp
}

// killNoteOf is the kill facts a stored response carries; none for no
// response.
func killNoteOf(resp *threatdetect.FindingResponse) killNote {
	if resp == nil {
		return killNote{}
	}
	return killNote{killed: resp.ProcessKilled, pid: resp.KilledPID, count: resp.Kills, skip: resp.KillSkipReason}
}

// killFacts folds this attempt's outcome into the kill facts cur carries: a
// kill adds to the count and clears an earlier skip; a skip is recorded; no
// attempt leaves them as they were. cur is the finding's current response as
// the caller already read it (under r.mu on the queued path), so kills on the
// same finding that were queued separately add up without a second read.
func killFacts(cur *threatdetect.FindingResponse, ko killOutcome) killNote {
	n := killNoteOf(cur)
	switch {
	case ko.pid != 0:
		n.killed, n.pid, n.count, n.skip = true, ko.pid, n.count+1, pb.KillSkipReason_KILL_SKIP_REASON_UNSPECIFIED
	case ko.skip != pb.KillSkipReason_KILL_SKIP_REASON_UNSPECIFIED:
		n.skip = ko.skip
	}
	return n
}

// releaseKill is the kill facts a release, expiry or orphan-sweep write
// carries (#2772: the record is per box). A box that killed nothing writes no
// kill of its own; the store keeps the finding's kill on record. A box that
// did kill writes the finding's current latest pid and count, not the first
// kill's pid its action row holds, and never a kill with pid 0. A skip reason
// on record is the finding's current state, not a kill, so it is carried
// either way (#2775 review): a THROTTLE whose kill was refused keeps its
// reason on the RELEASED and EXPIRED writes.
func (r *ProcessResponder) releaseKill(ctx context.Context, row *threatdetect.SecurityAction) killNote {
	var cur killNote
	if g, err := r.findings.Get(ctx, row.FindingID); err == nil {
		cur = killNoteOf(g.Response)
	}
	if row.KilledPID == 0 {
		return killNote{skip: cur.skip}
	}
	if cur.killed && cur.pid != 0 {
		return cur
	}
	return killNote{killed: true, pid: row.KilledPID, count: 1}
}

// killSkipReasonOf classifies why the killer refused. The full error stays in
// the audit row; only the class reaches the webhook.
func killSkipReasonOf(err error) pb.KillSkipReason {
	switch {
	case errors.Is(err, errKillPIDReused):
		return pb.KillSkipReason_KILL_SKIP_REASON_PID_REUSED
	case errors.Is(err, errKillProcessGone):
		return pb.KillSkipReason_KILL_SKIP_REASON_PROCESS_GONE
	case errors.Is(err, errKillContainerInit):
		return pb.KillSkipReason_KILL_SKIP_REASON_CONTAINER_INIT
	case errors.Is(err, errKillStartTimeUnknown):
		return pb.KillSkipReason_KILL_SKIP_REASON_START_TIME_UNKNOWN
	case errors.Is(err, errKillRuntimeUnsupported):
		return pb.KillSkipReason_KILL_SKIP_REASON_RUNTIME_UNSUPPORTED
	}
	return pb.KillSkipReason_KILL_SKIP_REASON_OTHER
}

// effectivePolicyOnce returns a function that resolves container's effective
// policy on its first call and returns that same result (policy or error)
// on every later one. One finding's handling calls it from one goroutine,
// so it needs no lock.
func (r *ProcessResponder) effectivePolicyOnce(ctx context.Context, container string) func() (*pb.ProcessPolicy, error) {
	var (
		done   bool
		policy *pb.ProcessPolicy
		err    error
	)
	return func() (*pb.ProcessPolicy, error) {
		if !done {
			policy, err = r.policies.Effective(ctx, container)
			done = true
		}
		return policy, err
	}
}

// maybeKill is the kill_process step (#2638): for a miner-signature finding
// whose effective policy sets kill_process, SIGKILL the process of the exec
// that just matched (target) after the killer re-checks its
// cgroup against the container. Every attempt is audited — "kill" naming the
// pid, or "kill-skipped" with the reason (pid reused, already exited, ...).
// Returns the killed pid, 0 when nothing was killed. Off on a degraded
// backend like every other response (decided on #2641 Q2). Called without
// r.mu.
func (r *ProcessResponder) maybeKill(ctx context.Context, f *threatdetect.Finding, target *threatdetect.ExecEvidence, policyOf func() (*pb.ProcessPolicy, error)) killOutcome {
	if r.killer == nil || !threatdetect.KillsProcess(f.Rule) || target == nil || target.PID == 0 || r.degraded {
		return killOutcome{}
	}
	policy, err := policyOf()
	if err != nil {
		log.Printf("[process-responder] finding %d on %s: read policy for kill: %v", f.ID, f.Container, err)
		r.auditLog(ctx, "kill-skipped", f.Container, fmt.Sprintf("finding=%d rule=%s tenant=%s container=%s pid=%d kill_skipped=%q",
			f.ID, f.Rule, f.TenantID, f.Container, target.PID, "read policy: "+err.Error()))
		return killOutcome{skip: pb.KillSkipReason_KILL_SKIP_REASON_POLICY_UNREADABLE}
	}
	if !policy.GetKillProcess() {
		return killOutcome{}
	}
	ex := *target
	detail := fmt.Sprintf("finding=%d rule=%s tenant=%s container=%s pid=%d start_time=%d comm=%q filename=%q matched=%q",
		f.ID, f.Rule, f.TenantID, f.Container, ex.PID, ex.StartTime, ex.Comm, ex.Filename, ex.Matched)
	if err := r.killer.KillInContainer(f.Container, ex.PID, ex.StartTime); err != nil {
		r.auditLog(ctx, "kill-skipped", f.Container, fmt.Sprintf("%s kill_skipped=%q", detail, err.Error()))
		log.Printf("[process-responder] kill pid %d on %s (finding %d) skipped: %v", ex.PID, f.Container, f.ID, err)
		return killOutcome{skip: killSkipReasonOf(err)}
	}
	r.auditLog(ctx, "kill", f.Container, detail)
	log.Printf("[process-responder] SIGKILL pid %d (%s) on %s (finding %d, %s)", ex.PID, ex.Comm, f.Container, f.ID, ex.Matched)
	return killOutcome{pid: ex.PID}
}

// recordKill records a kill, or a skipped kill, on a finding the policy
// action leaves alone (another action already open, or a failed apply backing
// off): the stored response is kept and only the kill facts change. nil when
// there is nothing new to record — no kill was attempted, or the attempt left
// the facts as the response already has them (a repeat of the same skip) —
// so a re-fire with nothing new neither writes the store nor reaches the
