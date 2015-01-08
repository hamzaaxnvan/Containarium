package modelgateway

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sort"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Per-run record of the model calls the gateway proxied (#2928).
//
// A failure at the gateway (an upstream 401 on a bad key, a refused call, a
// dial error) happens outside the box and never reaches the run's journal, so
// the run reads as "command exited with code 1". The gateway records one
// typed event per call here; TailRunLog returns them next to the journal and a
// client interleaves them by time. The in-box journal stays single-writer.
//
// An event carries who paid (the owner TYPE) and how the call ended. It never
// carries a key, a key id, the token, the key owner's id, or any prompt or
// response content: there is no field for them.

// CallRecorder receives one event per proxied call. Nil in Config means calls
// are not recorded.
type CallRecorder interface {
	RecordCall(runID string, c *pb.GatewayCall)
}

const (
	// DefaultCallLogPerRun bounds the events kept for one run; the oldest go first.
	DefaultCallLogPerRun = 500
	// DefaultCallLogRuns bounds how many runs are kept; the least recently
	// written go first.
	DefaultCallLogRuns = 2000
	// DefaultCallLogTTL is how long a run's events outlive its last call.
	DefaultCallLogTTL = 24 * time.Hour
