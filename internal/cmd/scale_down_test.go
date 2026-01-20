package cmd

import (
	"strings"
	"testing"
)

// TestParseIdleMinutes table-drives the CLI's duration→minutes helper.
// Sub-minute durations and unparseable strings must return an error;
// empty string is the special "use default 15" sentinel for the flag's
// default value.
func TestParseIdleMinutes(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    int32
		wantErr bool
	}{
		{"empty → default 15", "", 15, false},
		{"1m → 1", "1m", 1, false},
		{"15m → 15", "15m", 15, false},
		{"1h → 60", "1h", 60, false},
		{"60m → 60 (same as 1h)", "60m", 60, false},
		{"1h30m → 90", "1h30m", 90, false},
		{"sub-minute rejected (30s)", "30s", 0, true},
		{"zero rejected", "0", 0, true},
		{"negative rejected", "-15m", 0, true},
		{"non-duration rejected", "abc", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseIdleMinutes(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseIdleMinutes(%q) = %d, nil; want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseIdleMinutes(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("parseIdleMinutes(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestParseIdleMinutes_ErrorMessages locks the operator-facing error
