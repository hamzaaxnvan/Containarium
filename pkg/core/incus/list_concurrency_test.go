package incus

import (
	"sort"
	"sync/atomic"
	"testing"
)

// TestFetchAllConcurrently_SkipsNotOk is the regression test for
// ListContainers's "skip containers we can't get details for" behavior
// (#1532's refactor from a sequential loop to a bounded worker pool must not
// change what gets returned, only how fast).
func TestFetchAllConcurrently_SkipsNotOk(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e"}
	skip := map[string]bool{"b": true, "d": true}

	got := fetchAllConcurrently(names, 4, func(name string) (string, bool) {
		if skip[name] {
			return "", false
		}
		return name, true
	})

	sort.Strings(got)
	want := []string{"a", "c", "e"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// TestFetchAllConcurrently_RespectsConcurrencyLimit is the regression test
// for #1532 itself: ListContainers used to make every GetInstance +
// GetInstanceState round-trip one after another, which made `containarium
// list` exceed its client deadline past ~180 containers on one host. This
// asserts the fan-out actually bounds in-flight work rather than firing
