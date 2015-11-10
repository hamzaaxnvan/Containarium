package tracker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// A cancelled tick must never strand a row (#2049): between winning the
// insert and a successful StartRun, every cancellation point ends with
// the row removed (nothing on the forge yet) or FAILED (projected), and
// a later tick can dispatch the issue again. After a successful start
// the row stays with its live run and no later tick starts a second one.

// cancelPoint names where in the tick the context is cancelled.
type cancelPoint int

const (
	cancelAfterInsert       cancelPoint = iota + 1 // the insert has returned
	cancelInsideReread                             // inside the post-insert GetIssue
	cancelInsideQueuedLabel                        // inside the agent:queued SetLabels
	cancelAfterQueuedLabel                         // the label write has returned
	cancelInsideStartRun                           // StartRun sees the cancel and fails
	cancelAfterStart                               // StartRun succeeded, then the tick is cancelled
)

// cancellingProvider cancels the tick once, at the chosen point. A real
// forge client honours its context, so a call that saw the cancel fails
// with it; forgeErr additionally makes the label write fail on its own.
type cancellingProvider struct {
	*fakeDispatchProvider
	cancel   context.CancelFunc
	at       cancelPoint
	forgeErr error

	mu    sync.Mutex
	fired bool
}

func (p *cancellingProvider) fire(at cancelPoint) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fired || p.at != at {
		return false
	}
	p.fired = true
	p.cancel()
	return true
}

func (p *cancellingProvider) GetIssue(ctx context.Context, c Conn, number int64) (Issue, error) {
	if p.fire(cancelInsideReread) {
		if err := ctx.Err(); err != nil {
			return Issue{}, err
		}
	}
	return p.fakeDispatchProvider.GetIssue(ctx, c, number)
}

func (p *cancellingProvider) SetLabels(ctx context.Context, c Conn, number int64, add, remove []string) error {
	if !containsString(add, LabelAgentQueued) {
		return p.fakeDispatchProvider.SetLabels(ctx, c, number, add, remove)
	}
	if p.fire(cancelInsideQueuedLabel) {
		if p.forgeErr != nil {
			return p.forgeErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	err := p.fakeDispatchProvider.SetLabels(ctx, c, number, add, remove)
	p.fire(cancelAfterQueuedLabel)
	return err
}

// cancelAfterInsertStore cancels the tick as the insert returns.
type cancelAfterInsertStore struct {
	DispatchStore
	p *cancellingProvider
}

func (s cancelAfterInsertStore) InsertDispatch(ctx context.Context, d Dispatch) (*Dispatch, error) {
	row, err := s.DispatchStore.InsertDispatch(ctx, d)
	s.p.fire(cancelAfterInsert)
	return row, err
}

// ctxRunStarter honours its context the way provisioning does, records
// every call and which runs really started, and can cancel the tick
// inside the call (before failing) or after a successful start.
type ctxRunStarter struct {
	p           *cancellingProvider
	reportStart bool

	mu      sync.Mutex
	calls   int
	started map[string]bool
}

func (s *ctxRunStarter) StartRun(ctx context.Context, req StartRunRequest) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if s.p != nil {
		s.p.fire(cancelInsideStartRun)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.started == nil {
		s.started = map[string]bool{}
	}
	s.started[req.RunID] = true
	s.mu.Unlock()
	if s.p != nil && s.p.fire(cancelAfterStart) && s.reportStart {
		// Production reports the start on the tick's own (now cancelled)
		// context: launchDispatchedRun calls RunStarted(ctx).
		req.Lifecycle.RunStarted(ctx)
	}
	return nil
}

func (s *ctxRunStarter) snapshot() (int, map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	for k, v := range s.started {
		out[k] = v
	}
	return s.calls, out
}
