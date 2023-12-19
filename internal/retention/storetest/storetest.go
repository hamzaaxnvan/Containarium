// Package storetest is the behavioural contract every retention.Store must
// meet. The in-memory store runs it in the unit suite; the Postgres store
// runs it in the store-integration lane against a real database.
package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/footprintai/containarium/internal/retention"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func window(c pb.RetentionClass, days int32) *pb.RetentionWindow {
	return &pb.RetentionWindow{Class: c, Days: days}
}

// days returns the policy's window per class.
func days(t *testing.T, p *pb.RetentionPolicy) map[pb.RetentionClass]*pb.RetentionWindow {
	t.Helper()
	out := map[pb.RetentionClass]*pb.RetentionWindow{}
	for _, w := range p.GetWindows() {
		out[w.GetClass()] = w
	}
	return out
}

// RunStoreContract runs the shared behaviour against stores built by newStore
// (each subtest gets a fresh, empty store).
func RunStoreContract(t *testing.T, newStore func(t *testing.T) retention.Store) {
	ctx := context.Background()

	t.Run("a fresh store reports the Q6 default for every class", func(t *testing.T) {
		p, err := newStore(t).GetPolicy(ctx)
		if err != nil {
			t.Fatalf("GetPolicy: %v", err)
		}
		want := map[pb.RetentionClass]int32{
			pb.RetentionClass_RETENTION_CLASS_PROMPTS:             30,
			pb.RetentionClass_RETENTION_CLASS_RESPONSES:           30,
			pb.RetentionClass_RETENTION_CLASS_GATEWAY_LOGS:        90,
			pb.RetentionClass_RETENTION_CLASS_SESSION_TRANSCRIPTS: 90,
			pb.RetentionClass_RETENTION_CLASS_EVIDENCE_BUNDLES:    365,
		}
		if len(p.GetWindows()) != len(want) {
			t.Fatalf("windows = %v, want one per class (%d)", p.GetWindows(), len(want))
		}
		for i, w := range p.GetWindows() {
			if w.GetClass() != retention.Classes()[i] {
				t.Fatalf("window %d is %v, want %v (enum order)", i, w.GetClass(), retention.Classes()[i])
			}
			if w.GetDays() != want[w.GetClass()] || !w.GetIsDefault() {
				t.Errorf("%v = %d days (default %v), want the default %d", w.GetClass(), w.GetDays(), w.GetIsDefault(), want[w.GetClass()])
			}
		}
		if p.GetRevision() != 0 {
			t.Errorf("revision = %d, want 0 before any set", p.GetRevision())
		}
	})

	t.Run("set changes only the listed classes and round-trips", func(t *testing.T) {
		s := newStore(t)
		res, err := s.SetPolicy(ctx, []*pb.RetentionWindow{
			window(pb.RetentionClass_RETENTION_CLASS_PROMPTS, 7),
			window(pb.RetentionClass_RETENTION_CLASS_EVIDENCE_BUNDLES, 400),
		}, "admin-a", nil)
		if err != nil {
			t.Fatalf("SetPolicy: %v", err)
		}
		if res.Previous.GetRevision() != 0 || res.Current.GetRevision() != 1 {
			t.Fatalf("revisions = %d -> %d, want 0 -> 1", res.Previous.GetRevision(), res.Current.GetRevision())
		}
		got, err := s.GetPolicy(ctx)
		if err != nil {
			t.Fatalf("GetPolicy: %v", err)
		}
		w := days(t, got)
		if d := w[pb.RetentionClass_RETENTION_CLASS_PROMPTS]; d.GetDays() != 7 || d.GetIsDefault() {
			t.Errorf("prompts = %v, want 7 days, set", d)
		}
		if d := w[pb.RetentionClass_RETENTION_CLASS_EVIDENCE_BUNDLES]; d.GetDays() != 400 || d.GetIsDefault() {
			t.Errorf("evidence bundles = %v, want 400 days, set", d)
		}
		if d := w[pb.RetentionClass_RETENTION_CLASS_RESPONSES]; d.GetDays() != 30 || !d.GetIsDefault() {
			t.Errorf("responses = %v, want the untouched default", d)
		}
		if got.GetRevision() != 1 || got.GetUpdatedBy() != "admin-a" || got.GetUpdatedAt() == nil {
			t.Errorf("bookkeeping = rev %d by %q at %v, want rev 1 by admin-a with a time", got.GetRevision(), got.GetUpdatedBy(), got.GetUpdatedAt())
		}
		if _, err := s.SetPolicy(ctx, []*pb.RetentionWindow{window(pb.RetentionClass_RETENTION_CLASS_PROMPTS, 8)}, "admin-b", nil); err != nil {
			t.Fatalf("second SetPolicy: %v", err)
		}
		got, _ = s.GetPolicy(ctx)
		if got.GetRevision() != 2 || days(t, got)[pb.RetentionClass_RETENTION_CLASS_EVIDENCE_BUNDLES].GetDays() != 400 {
			t.Errorf("after a second set: %v, want revision 2 with evidence bundles still 400", got)
		}
	})

	t.Run("an invalid set is rejected and changes nothing", func(t *testing.T) {
		cases := map[string][]*pb.RetentionWindow{
			"empty":                nil,
			"zero days":            {window(pb.RetentionClass_RETENTION_CLASS_PROMPTS, 0)},
			"negative days":        {window(pb.RetentionClass_RETENTION_CLASS_PROMPTS, -1)},
			"one day over the cap": {window(pb.RetentionClass_RETENTION_CLASS_PROMPTS, retention.MaxDays+1)},
