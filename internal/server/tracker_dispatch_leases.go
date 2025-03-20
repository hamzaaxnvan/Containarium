package server

import (
	"context"
	"log"
	"strings"
	"sync"

	"github.com/footprintai/containarium/internal/runlease"
	"github.com/footprintai/containarium/internal/tracker"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

// Failure visibility, server half (#2026): the production RunStarter is
// also the dispatcher's tracker.RunLeases — which dispatched runs this
// daemon holds, and a way to end a timed-out one's lease — and the
// daemon emits the dispatcher's terminal events as OTel metrics.

// dispatchedRuns is the set of tracker-dispatched runs this daemon
// holds. A run enters it when StartRun begins (before provisioning) and
// leaves it only after its end has been reported to the dispatch row,
// so the sweep can never see a run that is merely finishing as lost.
type dispatchedRuns struct {
	mu   sync.Mutex
	runs map[string]*dispatchedRun
}

// dispatchedRun is one entry: its lease once provisioned, and whether
// that lease has been ended (by the run's own end or by the sweep —
// whichever comes first; the other is a no-op).
type dispatchedRun struct {
	mu    sync.Mutex
	lease *runlease.Lease
	ended bool
}

// track returns runID's entry, creating it if needed.
func (d *dispatchedRuns) track(runID string) *dispatchedRun {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.runs == nil {
		d.runs = map[string]*dispatchedRun{}
	}
	h, ok := d.runs[runID]
