package retention

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// removerSource is a fakeSource that can also delete. It records every Remove.
type removerSource struct {
	fakeSource
	removed   int64
	skipped   string
	removeErr error
	calls     []removeCall
	log       *[]string // shared event log, to assert ordering
}

type removeCall struct {
	cutoff  time.Time
	held    []string
	maxRows int
}

func (r *removerSource) Remove(_ context.Context, cutoff time.Time, held []string, maxRows int) (Removal, error) {
	r.calls = append(r.calls, removeCall{cutoff, held, maxRows})
	if r.log != nil {
		*r.log = append(*r.log, "remove:"+r.store)
	}
	return Removal{Removed: r.removed, Skipped: r.skipped}, r.removeErr
}

// auditedSweeper is a sweeper with an auditor that accepts every entry, the
// shape of a daemon with an audit store.
func auditedSweeper(t *testing.T, store Store, srcs []Source, unswept []Unswept) *Sweeper {
	t.Helper()
	sw := newTestSweeper(t, store, srcs, unswept)
	sw.SetAuditor(func(context.Context, *pb.RetentionReport) error { return nil })
	return sw
}

func enforcing(t *testing.T) *MemoryStore {
	t.Helper()
	st := NewMemoryStore()
	if _, err := st.SetMode(context.Background(), pb.RetentionMode_RETENTION_MODE_ENFORCE, "admin", nil); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestSweeper_DryRunIsTheDefaultAndNeverRemoves(t *testing.T) {
	src := &removerSource{fakeSource: fakeSource{class: pb.RetentionClass_RETENTION_CLASS_PROMPTS, store: "agent_tasks", count: Count{Older: 3, Eligible: 3}}, removed: 3}
	rep, err := newTestSweeper(t, NewMemoryStore(), []Source{src}, nil).Sweep(context.Background())
	if err != nil {
