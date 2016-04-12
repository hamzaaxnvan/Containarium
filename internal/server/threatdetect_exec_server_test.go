package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/netbpf"
	"github.com/footprintai/containarium/internal/threatdetect"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeExecSource stands in for the ExecWatcher behind the server.
type fakeExecSource struct {
	status string
	lost   int64
	replay []ExecEventRecord
	live   chan ExecEventRecord
	subs   []struct {
		container string
		since     time.Duration
	}
	cancelled int
}

func (f *fakeExecSource) Status() string { return f.status }
func (f *fakeExecSource) Lost() int64    { return f.lost }
func (f *fakeExecSource) Subscribe(container string, since time.Duration, _ int) ([]ExecEventRecord, <-chan ExecEventRecord, func()) {
	f.subs = append(f.subs, struct {
		container string
		since     time.Duration
	}{container, since})
	var out []ExecEventRecord
	for _, r := range f.replay {
		if container == "" || r.Container == container {
			out = append(out, r)
		}
	}
	return out, f.live, func() { f.cancelled++ }
}

// fakeExecStream captures what the handler sends. Send runs on the handler's
// goroutine while the follow test reads from its own, so the slice is
// guarded.
type fakeExecStream struct {
	grpc.ServerStream
	ctx  context.Context
	mu   sync.Mutex
	sent []*pb.ExecEvent
	err  error
}

func (s *fakeExecStream) Context() context.Context { return s.ctx }
func (s *fakeExecStream) Send(resp *pb.StreamExecEventsResponse) error {
