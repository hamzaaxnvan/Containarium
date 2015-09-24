package threatdetect

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func sampleAction(container string, at time.Time) *SecurityAction {
	return &SecurityAction{
		FindingID:        7,
		Container:        container,
		TenantID:         "tenant-a",
		Action:           pb.ProcessAction_PROCESS_ACTION_THROTTLE,
		PrevCPUCount:     "2",
		PrevCPUAllowance: "200ms/100ms",
		AppliedAt:        at,
		ExpiresAt:        at.Add(24 * time.Hour),
	}
}

// MemActionStore mirrors the security_actions table and its
// one-open-action-per-container partial unique index.
func TestMemActionStore_OnePerContainer(t *testing.T) {
	ctx := context.Background()
	s := NewMemActionStore()
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

	if _, err := s.Open(ctx, "box-a"); !errors.Is(err, ErrNoOpenAction) {
		t.Fatalf("Open on empty store = %v, want ErrNoOpenAction", err)
	}

	a := sampleAction("box-a", t0)
	if err := s.Insert(ctx, a); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if a.ID == 0 {
		t.Fatal("Insert did not assign an id")
	}
	if err := s.Insert(ctx, sampleAction("box-a", t0)); !errors.Is(err, ErrActionOpen) {
		t.Fatalf("second Insert for the same container = %v, want ErrActionOpen", err)
	}
	if err := s.Insert(ctx, sampleAction("box-b", t0)); err != nil {
		t.Fatalf("Insert for another container: %v", err)
	}

	got, err := s.Open(ctx, "box-a")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got.ID != a.ID || got.PrevCPUCount != "2" || got.PrevCPUAllowance != "200ms/100ms" {
		t.Fatalf("Open = %+v, want the inserted row", got)
	}

	if err := s.Release(ctx, a.ID, ReleasedByOperator, t0.Add(time.Hour)); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := s.Open(ctx, "box-a"); !errors.Is(err, ErrNoOpenAction) {
