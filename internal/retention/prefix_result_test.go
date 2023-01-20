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
