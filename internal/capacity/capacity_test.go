package capacity

import (
	"testing"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

func at(hour int) time.Time {
	return time.Date(2026, 6, 14, hour, 0, 0, 0, time.UTC)
}

func TestPolicyWindowOpen(t *testing.T) {
	cases := []struct {
		name string
		p    Policy
		hour int
		open bool
	}{
		{"always open (equal)", Policy{}, 3, true},
		{"day window in", Policy{WindowStartHour: 9, WindowEndHour: 17}, 12, true},
		{"day window before", Policy{WindowStartHour: 9, WindowEndHour: 17}, 8, false},
		{"day window at end exclusive", Policy{WindowStartHour: 9, WindowEndHour: 17}, 17, false},
		{"overnight in late", Policy{WindowStartHour: 22, WindowEndHour: 6}, 23, true},
		{"overnight in early", Policy{WindowStartHour: 22, WindowEndHour: 6}, 2, true},
		{"overnight out midday", Policy{WindowStartHour: 22, WindowEndHour: 6}, 12, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.p.WindowOpen(at(c.hour)); got != c.open {
				t.Fatalf("WindowOpen(%d) = %v, want %v", c.hour, got, c.open)
			}
		})
	}
}

func TestComputeSpareWithReserve(t *testing.T) {
	st := HostState{
		AvailableCPUs:        10,
		AvailableMemoryBytes: 1000,
		AvailableDiskBytes:   2000,
		Now:                  at(12),
	}
	h := Compute(Policy{ReserveFraction: 0.2}, st)
	if h.SpareCPUs != 8 {
		t.Errorf("SpareCPUs = %d, want 8", h.SpareCPUs)
	}
	if h.SpareMemoryBytes != 800 {
		t.Errorf("SpareMemoryBytes = %d, want 800", h.SpareMemoryBytes)
	}
	if h.SpareDiskBytes != 1600 {
		t.Errorf("SpareDiskBytes = %d, want 1600", h.SpareDiskBytes)
	}
}

func TestComputeWindowClosedZeroesSpare(t *testing.T) {
	st := HostState{
		AvailableCPUs:        10,
		AvailableMemoryBytes: 1000,
		AvailableDiskBytes:   2000,
		Now:                  at(3),
	}
	h := Compute(Policy{WindowStartHour: 9, WindowEndHour: 17}, st)
