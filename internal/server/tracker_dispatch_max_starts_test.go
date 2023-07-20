package server

import (
	"context"
	"testing"

	"github.com/footprintai/containarium/internal/tracker"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// max_starts (#2270) through the RPC: the request bounds the tick, the
// response reports how many eligible issues were left for a later tick.

func TestDispatchTrackerIssues_MaxStartsThroughRPC(t *testing.T) {
	const user = "tracker-dispatch-rpc-max-starts"
	var issues []tracker.Issue
	for n := int64(1); n <= 3; n++ {
		issues = append(issues, tracker.Issue{Number: n, State: pb.TrackerIssueState_TRACKER_ISSUE_STATE_OPEN, Labels: []string{"scope:product"}})
	}
	// issues[0] is also what the post-insert re-read sees: open, routed.
	provider := &fakeWriterProvider{fakeReaderProvider: fakeReaderProvider{issues: issues, issue: issues[0]}}
	ctx := context.Background()
	_ = mustTestTrackerStore(t).Delete(ctx, user, "default")
	s, _ := setUpWriterConnection(t, user, provider)
	if _, err := s.trackerStore.SetRoute(ctx, tracker.Route{Username: user, Connection: "default", Scope: "product", SkillID: "product-define"}); err != nil {
		t.Fatalf("SetRoute: %v", err)
	}
	starter := &recordingRunStarter{}
	s.SetTrackerRunStarter(starter)
	admin := kmsKeyTestCtx(user, "member", "tracker:admin,agents:run")

	resp, err := s.DispatchTrackerIssues(admin, &pb.DispatchTrackerIssuesRequest{Username: user, Connection: "default", MaxStarts: 1})
	if err != nil {
		t.Fatalf("DispatchTrackerIssues: %v", err)
