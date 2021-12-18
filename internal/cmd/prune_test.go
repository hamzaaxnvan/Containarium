package cmd

import (
	"sort"
	"testing"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

func names(cs []incus.ContainerInfo) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return out
}

// TestFilterForPrune covers the selection logic that decides which containers
// a `prune` would delete — the safety-critical part of bulk delete.
func TestFilterForPrune(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	fleet := []incus.ContainerInfo{
		{Name: "core-pg", Role: incus.RolePostgres, State: "Running", CreatedAt: now.Add(-72 * time.Hour)},
		{Name: "run-old", State: "Running", CreatedAt: now.Add(-2 * time.Hour), Labels: map[string]string{"managed_by": "ci"}},
		{Name: "run-new", State: "Running", CreatedAt: now.Add(-10 * time.Minute)},
		{Name: "stop-old", State: "Stopped", CreatedAt: now.Add(-3 * time.Hour)},
		{Name: "stop-new", State: "Stopped", CreatedAt: now.Add(-5 * time.Minute)},
		{Name: "pr-42-box", State: "Running", CreatedAt: now.Add(-26 * time.Hour)},
		{Name: "no-age", State: "Stopped"}, // CreatedAt zero
	}

	cases := []struct {
		name         string
		state        string
		nameContains string
		olderThan    time.Duration
		labels       map[string]string
		want         []string
	}{
