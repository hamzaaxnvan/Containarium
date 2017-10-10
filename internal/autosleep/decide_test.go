package autosleep

import (
	"strings"
	"testing"
	"time"
)

// TestDecide_TableDriven covers each of Decide's rules in order. Each
// case is a one-line statement of the rule it locks down so a future
// reader can find the rule by grepping the want.action / want.reason.
func TestDecide_TableDriven(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	mkInput := func() DecideInput {
		return DecideInput{
			Username:             "alice",
			State:                "Running",
			AutoSleepEnabled:     true,
			IdleThresholdMinutes: 15,
			IsCoreRole:           false,
			LastStartedAt:        now.Add(-2 * time.Hour),
			LastNetworkActivity:  now.Add(-5 * time.Minute),
			Now:                  now,
		}
	}

	tests := []struct {
		name        string
		mutate      func(*DecideInput)
		wantAction  DecideAction
		wantReason  string // substring match
		wantIdleGTE int    // 0 disables check; sleep cases set this
	}{
		{
			name:       "rule1_disabled",
			mutate:     func(in *DecideInput) { in.AutoSleepEnabled = false },
			wantAction: ActionNothing,
			wantReason: "not enabled",
		},
		{
			name:       "rule2_core_role_never_sleeps",
			mutate:     func(in *DecideInput) { in.IsCoreRole = true },
			wantAction: ActionNothing,
			wantReason: "core container",
		},
		{
			name:       "rule3_stopped_container_skipped",
			mutate:     func(in *DecideInput) { in.State = "Stopped" },
			wantAction: ActionNothing,
			wantReason: "Stopped",
		},
		{
			name: "rule4_anti_thrash_recently_started",
			mutate: func(in *DecideInput) {
				// 5m after start, threshold=15m; 2× threshold = 30m window.
				in.LastStartedAt = now.Add(-5 * time.Minute)
				in.LastNetworkActivity = now.Add(-1 * time.Hour) // idle is huge, but the window protects us.
			},
			wantAction: ActionNothing,
			wantReason: "recently started",
		},
		{
			name: "rule5_no_network_signal_no_start_undecidable",
			mutate: func(in *DecideInput) {
				in.LastStartedAt = time.Time{}
				in.LastNetworkActivity = time.Time{}
			},
			wantAction: ActionNothing,
			wantReason: "no traffic signal",
		},
		{
			name: "rule5_no_network_signal_with_start_above_threshold",
			mutate: func(in *DecideInput) {
				in.LastNetworkActivity = time.Time{}
				in.LastStartedAt = now.Add(-45 * time.Minute) // outside the 30m anti-thrash, 45m idle
			},
			wantAction:  ActionSleep,
			wantReason:  "no network signal",
			wantIdleGTE: 45,
		},
		{
			name: "rule6_idle_below_threshold",
			mutate: func(in *DecideInput) {
				in.LastNetworkActivity = now.Add(-5 * time.Minute) // 5m < 15m
			},
			wantAction: ActionNothing,
			wantReason: "below threshold",
		},
		{
			name: "rule6_idle_at_threshold_sleeps",
			mutate: func(in *DecideInput) {
				in.LastNetworkActivity = now.Add(-15 * time.Minute) // exactly threshold
			},
			wantAction:  ActionSleep,
			wantReason:  "idle 15m >= threshold 15m",
			wantIdleGTE: 15,
		},
