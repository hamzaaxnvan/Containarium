package audit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// Trenyx audit finding #4 (2026-09-16): the async writer paths (HTTP
// middleware, gRPC interceptor, event subscriber) logged a persistence
// failure and moved on, with nothing counting it — a row that was never
// written was otherwise undetectable. These tests prove both failure
// modes (a Log() error, and a full buffered channel dropping an entry
// before Log() was even attempted) now increment the shared counter.

func TestPersistFailureReport_StartsAtZero(t *testing.T) {
	ResetPersistFailuresForTest()
	r := GetPersistFailureReport()
	if r.Count != 0 {
		t.Fatalf("Count = %d, want 0", r.Count)
	}
	if !r.Since.IsZero() {
		t.Fatalf("Since = %v, want zero", r.Since)
	}
}

func TestPersistFailureReport_CountsAndLatchesFirstTimestamp(t *testing.T) {
	ResetPersistFailuresForTest()
	t.Cleanup(ResetPersistFailuresForTest)

	recordPersistFailure()
	first := GetPersistFailureReport()
	if first.Count != 1 {
		t.Fatalf("Count = %d, want 1", first.Count)
	}
	if first.Since.IsZero() {
		t.Fatal("Since is zero after the first failure, want a timestamp")
	}
