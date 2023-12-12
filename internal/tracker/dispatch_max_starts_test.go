package tracker

import (
	"context"
	"errors"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// max_starts (#2270): a caller enforcing its own budget bounds how many
// runs one tick starts. Issues past the limit get no row and no label,
// so a later tick picks them up.

func fiveEligibleIssues() []Issue {
	var out []Issue
	for n := int64(1); n <= 5; n++ {
		out = append(out, Issue{Number: n, Labels: []string{"scope:product"}})
	}
	return out
}

// dispatchedIssues is the set of issues that have any dispatch row.
func dispatchedIssues(t *testing.T, store *Store, ctx context.Context, user string) map[int64]bool {
	t.Helper()
	rows, err := store.ListDispatches(ctx, user, "default", pb.TrackerDispatchState_TRACKER_DISPATCH_STATE_UNSPECIFIED)
	if err != nil {
		t.Fatalf("ListDispatches: %v", err)
	}
	out := map[int64]bool{}
	for _, r := range rows {
		out[r.IssueNumber] = true
	}
	return out
}

func TestDispatch_MaxStartsBoundsTheTick(t *testing.T) {
	const user = "tracker-dispatch-max-starts"
