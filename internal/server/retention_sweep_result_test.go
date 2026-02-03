package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/retention"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// chainPrefix is a PrefixSource over a fixed answer for the hash-chained
// audit_logs: a purge that starts from base 10 and ends on base 510.
type chainPrefix struct {
	removals int
	removal  retention.PrefixRemoval
}

func baseOf(id int64) *pb.RetentionChainBase {
	return &pb.RetentionChainBase{BaseId: id, BaseHash: "h", PurgedAt: timestamppb.Now(), RowsPurged: id}
}

func newChainPrefix() *chainPrefix {
	return &chainPrefix{removal: retention.PrefixRemoval{
		RemovedByClass: map[pb.RetentionClass]int64{
			pb.RetentionClass_RETENTION_CLASS_GATEWAY_LOGS:        7,
			pb.RetentionClass_RETENTION_CLASS_SESSION_TRANSCRIPTS: 3,
		},
		Base: baseOf(510),
	}}
}

func (c *chainPrefix) Store() string { return "audit_logs" }
func (c *chainPrefix) Classes() []pb.RetentionClass {
	return []pb.RetentionClass{pb.RetentionClass_RETENTION_CLASS_GATEWAY_LOGS, pb.RetentionClass_RETENTION_CLASS_SESSION_TRANSCRIPTS}
}
func (c *chainPrefix) CountPrefix(context.Context, map[pb.RetentionClass]time.Time, []string) (retention.PrefixCount, error) {
	return retention.PrefixCount{
		ByClass: map[pb.RetentionClass]retention.Count{
			pb.RetentionClass_RETENTION_CLASS_GATEWAY_LOGS:        {Older: 9, Eligible: 7},
			pb.RetentionClass_RETENTION_CLASS_SESSION_TRANSCRIPTS: {Older: 4, Eligible: 3},
		},
		Base: baseOf(10),
	}, nil
}
func (c *chainPrefix) RemovePrefix(context.Context, map[pb.RetentionClass]time.Time, []string, int) (retention.PrefixRemoval, error) {
	c.removals++
	return c.removal, nil
}

// failingAction is an audit logger that refuses one action and records the rest.
type failingAction struct {
	*fakeAuditLogger
	action string
}

func (f failingAction) Log(ctx context.Context, e *audit.AuditEntry) error {
	if e.Action == f.action {
		return errors.New("audit store down")
	}
	return f.fakeAuditLogger.Log(ctx, e)
}

// sweepOnce runs one start-up sweep of the reporter, as the daemon does.
func sweepOnce(t *testing.T, r *retention.Reporter) *pb.RetentionReport {
	t.Helper()
	prev := r.Last()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx, time.Hour); close(done) }()
	deadline := time.After(5 * time.Second)
	for r.Last() == prev {
		select {
		case <-deadline:
			t.Fatal("no sweep ran")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
	return r.Last()
}

// purgeServer wires a RetentionServer, an audit logger and a reporter over the
// fake chain, in the given mode.
func purgeServer(t *testing.T, mode pb.RetentionMode, logger auditLogger) (*retention.Reporter, *chainPrefix) {
	t.Helper()
	store := retention.NewMemoryStore()
	if _, err := store.SetMode(context.Background(), mode, "ops", nil); err != nil {
		t.Fatal(err)
	}
	chain := newChainPrefix()
	reporter := retention.NewReporter(retention.NewSweeper(store, nil, nil))
	reporter.AddPrefixSource(chain)
