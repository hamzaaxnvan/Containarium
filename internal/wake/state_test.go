package wake

import (
	"fmt"
	"sync"
	"testing"
)

// TestWakeStateTracker_BasicConcurrency — 10 goroutines independently
// mark + clear different container names, plus a race-y reader. After
// all goroutines finish, the tracker should be empty (every Mark was
// paired with a Clear). Run with `-race` to also catch the obvious
// "forgot the lock" bugs in the map.
func TestWakeStateTracker_BasicConcurrency(t *testing.T) {
	tracker := New()

	const goroutines = 10
	const iterations = 100
