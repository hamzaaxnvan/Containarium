package anonbox

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// LabelWarned records which expiry warnings a box has already been sent,
// as a comma-separated list of threshold names ("10m", "1m"). It lives on
// the box, not in memory, so a daemon restart never re-sends one (#2202).
const LabelWarned = "anon.warned"

// warnThresholds are the warnings, longest first. A threshold fires once
// the remaining TTL is at or under it; because Warn runs on a one-minute
// ticker, "T-10m" lands between 9 and 10 minutes before expiry.
var warnThresholds = []struct {
	name   string
	before time.Duration
}{
	{"10m", 10 * time.Minute},
	{"1m", time.Minute},
}

// Warn sends the expiry warnings that are due to every live, unclaimed
// anonymous box: a `wall` into the guest at T-10m and at T-1m, each at
// most once. A warning counts as sent once attempted — a failed `wall` is
// logged and marked, never retried: the ticker is the only retry cadence
// and a box that cannot run `wall` will not start running it in a minute.
func (m *Manager) Warn(ctx context.Context) error {
	all, err := m.boxes.List(ctx)
	if err != nil {
		return fmt.Errorf("anonbox: list boxes: %w", err)
	}
	now := m.cfg.Now()
	for i := range all {
		st := &all[i]
		if !IsUnclaimedAnonymous(st.Labels) || st.TTLExpiresAt.IsZero() {
			continue
		}
		remaining := st.TTLExpiresAt.Sub(now)
		if remaining <= 0 {
			continue // the sweeper's problem now
		}
		m.remind(ctx, st.Ref.Name, st.Labels, st.TTLExpiresAt, now)
