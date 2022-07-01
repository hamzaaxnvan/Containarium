package runner

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// -----------------------------------------------------------------
// MaxRunnersTotal env parsing
// -----------------------------------------------------------------

func TestMaxRunnersTotal(t *testing.T) {
	cases := []struct {
		name string
		env  string
		set  bool
		want int
	}{
		{"unset -> default", "", false, DefaultMaxRunnersTotal},
		{"empty -> default", "", true, DefaultMaxRunnersTotal},
		{"valid", "7", true, 7},
		{"whitespace", "  12 ", true, 12},
		{"garbage -> default", "abc", true, DefaultMaxRunnersTotal},
		{"zero -> default", "0", true, DefaultMaxRunnersTotal},
		{"negative -> default", "-5", true, DefaultMaxRunnersTotal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("MAX_RUNNERS_TOTAL", tc.env)
			} else {
				// Ensure no ambient value leaks in.
				t.Setenv("MAX_RUNNERS_TOTAL", "")
			}
			if got := MaxRunnersTotal(); got != tc.want {
				t.Errorf("MaxRunnersTotal() = %d, want %d", got, tc.want)
			}
		})
	}
}

// -----------------------------------------------------------------
// CountLiveRunners
// -----------------------------------------------------------------

func TestCountLiveRunners(t *testing.T) {
	boxes := &fakeBoxes{
		listOut: []string{"ci-runner-1", "ci-runner-2", "alice", "bob", "ci-runner-x"},
	}
	n, err := CountLiveRunners(context.Background(), Deps{Boxes: boxes}, "ci-runner")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 ci-runner* boxes, got %d", n)
	}

	// Empty prefix falls back to the default "ci-runner".
	n, err = CountLiveRunners(context.Background(), Deps{Boxes: boxes}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 3 {
		t.Errorf("default-prefix count expected 3, got %d", n)
	}
}

// -----------------------------------------------------------------
// Provision cap enforcement
