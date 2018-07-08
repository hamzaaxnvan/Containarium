package tracker

import (
	"context"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// Chain guards on the dispatcher side (#2025; design
// docs/architecture/issue-triggered-agents.md, "Chain guards": "the
// dispatcher also refuses to start a run for an issue whose depth
// exceeds the max, so a policy lowered later still holds").

// seedChain records a lineage chain #root → #root+1 → … of n children,
// one run per hop, with no caps (a policy that allowed it at the time).
func seedChain(t *testing.T, store *Store, ctx context.Context, user string, root int64, n int) {
	t.Helper()
	if _, err := store.pool.Exec(ctx, "DELETE FROM tracker_issue_lineage WHERE username = $1", user); err != nil {
		t.Fatalf("clean lineage: %v", err)
	}
	for i := 1; i <= n; i++ {
		child := root + int64(i)
		if _, err := store.RecordChild(ctx, Lineage{
			Username: user, Connection: "default", ParentNumber: child - 1, CreatedByRun: "seed-run-" + string(rune('a'+i)),
		}, 0, 0, func(context.Context) (int64, error) { return child, nil }); err != nil {
			t.Fatalf("seed lineage #%d: %v", child, err)
		}
	}
}

// TestDispatch_SkipsOverDepth: with the default policy (max_depth 3) an
// issue at depth 3 is dispatched — and its row and run input carry that
