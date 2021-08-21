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
