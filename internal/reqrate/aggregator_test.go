package reqrate

import (
	"strings"
	"testing"
	"time"
)

func TestCounter_SnapshotComputesRateAndResets(t *testing.T) {
	c := NewCounter()
	for i := 0; i < 6; i++ {
		c.Add("alice.example.com")
	}
	for i := 0; i < 3; i++ {
		c.Add("bob.example.com")
	}

	got := c.Snapshot(30 * time.Second)
	if got["alice.example.com"] != 0.2 { // 6 / 30s
		t.Errorf("alice rate = %v, want 0.2", got["alice.example.com"])
	}
	if got["bob.example.com"] != 0.1 { // 3 / 30s
		t.Errorf("bob rate = %v, want 0.1", got["bob.example.com"])
	}

	// Second snapshot of an untouched counter is empty — the window reset.
	if got := c.Snapshot(30 * time.Second); got != nil {
		t.Errorf("second snapshot = %v, want nil after reset", got)
	}
}

func TestCounter_SnapshotGuards(t *testing.T) {
	c := NewCounter()
	c.Add("x")
