package ttlsweeper

import (
	"reflect"
	"sort"
	"testing"
	"time"
)

// ptr is a tiny helper so the table literals stay readable —
// `TTLExpiresAt: ptr(now.Add(-time.Hour))` reads better than
// declaring a local variable per case.
func ptr(t time.Time) *time.Time { return &t }

func TestDecide(t *testing.T) {
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		containers []ContainerView
		// want is order-independent; the test sorts both sides before
		// comparing. Decide doesn't guarantee any particular order
		// and we don't want the test to lock in an accidental order.
		want []string
	}{
		{
			name:       "empty list returns no names",
			containers: nil,
			want:       nil,
		},
		{
			name: "containers with no TTL are skipped",
			containers: []ContainerView{
				{Name: "alice", TTLExpiresAt: nil},
				{Name: "bob", TTLExpiresAt: nil},
			},
			want: nil,
		},
		{
			name: "all expired containers are returned",
			containers: []ContainerView{
				{Name: "alice", TTLExpiresAt: ptr(now.Add(-time.Hour))},
				{Name: "bob", TTLExpiresAt: ptr(now.Add(-5 * time.Minute))},
			},
			want: []string{"alice", "bob"},
		},
		{
			name: "all active (future) TTLs are skipped",
			containers: []ContainerView{
				{Name: "alice", TTLExpiresAt: ptr(now.Add(time.Hour))},
				{Name: "bob", TTLExpiresAt: ptr(now.Add(24 * time.Hour))},
			},
			want: nil,
		},
		{
