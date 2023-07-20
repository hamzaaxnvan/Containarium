package retention

import (
	"context"
	"errors"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// resultSweeper is an enforcing sweeper over p whose two audit hooks record
// their calls in order.
func resultSweeper(t *testing.T, p *fakePrefix, events *[]string, results *[]PurgeResult, resultErr error) *Sweeper {
	t.Helper()
	sw := newTestSweeper(t, enforcing(t), nil, chainUnswept())
	sw.SetAuditor(func(context.Context, *pb.RetentionReport) error { *events = append(*events, "audit"); return nil })
	sw.SetResultAuditor(func(_ context.Context, r PurgeResult) error {
		*events = append(*events, "result")
		*results = append(*results, r)
		return resultErr
	})
	p.log = events
	sw.AddPrefixSource(p)
	return sw
}

func TestPrefixResult_APurgeWritesTheResultAfterTheRemovalWithBothBases(t *testing.T) {
	var events []string
	var results []PurgeResult
	p := eligibleChain()
	sw := resultSweeper(t, p, &events, &results, nil)

	if _, err := sw.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if strings.Join(events, ",") != "audit,remove,result" {
		t.Fatalf("events = %v, want the result entry after the removal", events)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	r := results[0]
	if r.Store != "audit_logs" || r.PreviousBaseID != 10 || r.BaseID != 510 || r.RowsRemoved != 10 {
		t.Errorf("result = %+v, want audit_logs previous base 10, new base 510, 10 rows", r)
	}
	if r.ByClass[gwClass] != 7 || r.ByClass[sessClass] != 3 {
		t.Errorf("by class = %v, want gateway 7 and session 3", r.ByClass)
	}
}

func TestPrefixResult_NoPriorPurgeHasPreviousBaseZero(t *testing.T) {
	var events []string
	var results []PurgeResult
	p := eligibleChain()
	p.count.Base = nil
	sw := resultSweeper(t, p, &events, &results, nil)

	if _, err := sw.Sweep(context.Background()); err != nil {
