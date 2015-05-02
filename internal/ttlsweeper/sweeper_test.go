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
			name: "mixed: only expired ones are returned",
			containers: []ContainerView{
				{Name: "alice-no-ttl", TTLExpiresAt: nil},
				{Name: "bob-future", TTLExpiresAt: ptr(now.Add(time.Hour))},
				{Name: "carol-expired", TTLExpiresAt: ptr(now.Add(-time.Hour))},
				{Name: "dave-just-expired", TTLExpiresAt: ptr(now.Add(-time.Minute))},
				{Name: "eve-no-ttl", TTLExpiresAt: nil},
			},
			want: []string{"carol-expired", "dave-just-expired"},
		},
		{
			name: "exactly at cutoff (now - graceMargin) is treated as expired",
			containers: []ContainerView{
				{Name: "edge", TTLExpiresAt: ptr(now.Add(-graceMargin))},
			},
			want: []string{"edge"},
		},
		{
			name: "inside the grace window is NOT yet expired",
			// TTL was a tiny moment ago — still inside graceMargin,
			// so we wait for the next sweep. This is the whole point
			// of the grace margin: protect against clock skew between
			// the TTL-setter and the daemon host.
			containers: []ContainerView{
				{Name: "skewed", TTLExpiresAt: ptr(now.Add(-graceMargin / 2))},
			},
			want: nil,
		},
		{
			name: "TTL exactly at now is inside grace window — skipped",
			containers: []ContainerView{
				{Name: "right-now", TTLExpiresAt: ptr(now)},
			},
			want: nil,
		},
		{
			name: "TTL one nanosecond past the cutoff is skipped",
			containers: []ContainerView{
				{Name: "almost", TTLExpiresAt: ptr(now.Add(-graceMargin + time.Nanosecond))},
			},
			want: nil,
		},
		{
			name: "TTL set to the zero time is treated as long-expired",
			// Defensive: a caller setting *time.Time to &time.Time{}
			// rather than nil should not silently mean "never delete".
			// Decide treats the zero value as a past time (the unix
			// epoch is way before any plausible "now"), so the
			// container is flagged for deletion. The persistence layer
			// in the wiring PR is responsible for normalizing "clear
			// TTL" to a literal nil; this assertion locks in the
			// fail-loud behavior if it ever forgets.
			containers: []ContainerView{
				{Name: "zero", TTLExpiresAt: ptr(time.Time{})},
			},
