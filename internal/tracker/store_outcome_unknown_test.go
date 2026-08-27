package tracker

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// An ambiguous upstream create (#2045): the request reached the forge but
// no answer came back before the budget, so the issue may exist upstream.
// RecordChild must say so with ErrUpstreamOutcomeUnknown and keep the
// fan-out slot claimed, so a blind retry cannot file past the cap.
func TestRecordChild_AmbiguousCreateKeepsTheSlotAndSaysOutcomeUnknown(t *testing.T) {
	store, ctx := newTrackerTestStore(t)
	const user = "tracker-store-outcome-unknown"
	resetReservationFixture(t, store, ctx, user)
	l := Lineage{Username: user, Connection: "default", ParentNumber: 10, CreatedByRun: "run-slow-forge"}

	_, err := store.RecordChild(ctx, l, 0, 1, func(context.Context) (int64, error) {
		// What the adapters return when the HTTP client's timeout fires
		// after the POST was sent.
		return 0, fmt.Errorf("%w: %w", ErrUnreachable, context.DeadlineExceeded)
	})
	if !errors.Is(err, ErrUpstreamOutcomeUnknown) {
		t.Fatalf("RecordChild err = %v, want ErrUpstreamOutcomeUnknown", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("RecordChild err = %v, want the upstream cause kept in the chain", err)
	}
