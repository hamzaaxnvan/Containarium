package wake

import (
	"testing"
)

// TestWakeStateTracker_Snapshot_IsACopy — mutating the returned map
// must not leak into the tracker. The brief flagged this because some
// implementations return the live map by reference and confuse callers
// that iterate while writers race.
func TestWakeStateTracker_Snapshot_IsACopy(t *testing.T) {
	tracker := New()
	tracker.MarkWakeMode("alice-container", "alice.example.test", "10.0.3.1", 8080)
	snap := tracker.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot len = %d, want 1", len(snap))
	}
	// Mutate the snapshot.
	delete(snap, "alice-container")
	snap["bogus"] = WakeEntry{Subdomain: "bogus", WakeHost: "0.0.0.0"}

	// Tracker should still hold alice-container and not bogus.
	if _, _, ok := tracker.IsInWakeMode("alice-container"); !ok {
		t.Errorf("alice-container should still be in wake mode (snapshot was mutated)")
	}
	if _, _, ok := tracker.IsInWakeMode("bogus"); ok {
		t.Errorf("bogus should not be in wake mode (snapshot was mutated)")
	}
}

// TestWakeStateTracker_Entry — Entry(name) returns the full WakeEntry;
// unknown name returns the zero value + ok=false.
func TestWakeStateTracker_Entry(t *testing.T) {
	tracker := New()
	tracker.MarkWakeMode("alice-container", "alice.example.test", "10.0.3.1", 8080)

	got, ok := tracker.Entry("alice-container")
	if !ok {
		t.Fatal("alice-container should be present")
