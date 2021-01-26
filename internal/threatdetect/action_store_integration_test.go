//go:build integration

// Integration coverage for the Postgres security_actions store (#2636): the
// one-open-action-per-container partial unique index, release, and the TTL
// query are database behaviour, so they run against real Postgres in the
// same CONTAINARIUM_TEST_DSN lane as store_integration_test.go.
package threatdetect

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestActionStore_OpenPerContainerIndex(t *testing.T) {
	pool := threatdetectPool(t)
	fs, _, _ := newStores(t, pool)
	ctx := context.Background()
	as, err := NewActionStore(ctx, pool)
	if err != nil {
		t.Fatalf("NewActionStore: %v", err)
	}
	f, err := fs.Insert(ctx, sampleFinding())
	if err != nil {
		t.Fatalf("Insert finding: %v", err)
	}
	t0 := time.Now().UTC().Truncate(time.Microsecond)

	a := sampleAction("c-miner", t0)
	a.FindingID = f.ID
	if err := as.Insert(ctx, a); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if a.ID == 0 {
		t.Fatal("Insert did not assign an id")
	}
	dup := sampleAction("c-miner", t0)
	dup.FindingID = f.ID
	if err := as.Insert(ctx, dup); !errors.Is(err, ErrActionOpen) {
		t.Fatalf("second open row for the same container = %v, want ErrActionOpen (partial unique index)", err)
	}

	got, err := as.Open(ctx, "c-miner")
	if err != nil {
