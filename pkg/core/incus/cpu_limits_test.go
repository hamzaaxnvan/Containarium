package incus

import (
	"strings"
	"testing"
)

func TestParseCPULimit(t *testing.T) {
	tests := []struct {
		name          string
		cpu           string
		wantCount     string
		wantAllowance string
		wantErr       bool
	}{
		{name: "empty", cpu: "", wantCount: "", wantAllowance: ""},
		{name: "single core", cpu: "1", wantCount: "1", wantAllowance: "100ms/100ms"},
		{name: "multiple cores", cpu: "4", wantCount: "4", wantAllowance: "400ms/100ms"},
		{name: "whole host", cpu: "8", wantCount: "8", wantAllowance: "800ms/100ms"},
		{name: "cpu range", cpu: "0-3", wantCount: "0-3"},
		{name: "cpu set", cpu: "0,2-4", wantCount: "0,2-4"},
		{name: "millicpu quarter core", cpu: "250m", wantCount: "1", wantAllowance: "25ms/100ms"},
		{name: "millicpu half core", cpu: "500m", wantCount: "1", wantAllowance: "50ms/100ms"},
		{name: "millicpu whole core", cpu: "1000m", wantCount: "1", wantAllowance: "100ms/100ms"},
		{name: "millicpu one and a half", cpu: "1500m", wantCount: "2", wantAllowance: "150ms/100ms"},
		{name: "decimal quarter core", cpu: "0.25", wantCount: "1", wantAllowance: "25ms/100ms"},
		{name: "decimal whole core", cpu: "2.0", wantCount: "2", wantAllowance: "200ms/100ms"},
		{name: "decimal one and a half", cpu: "1.5", wantCount: "2", wantAllowance: "150ms/100ms"},
		{name: "decimal two and a half", cpu: "2.5", wantCount: "3", wantAllowance: "250ms/100ms"},
		{name: "whitespace trimmed", cpu: "  250m  ", wantCount: "1", wantAllowance: "25ms/100ms"},
		{name: "invalid millicpu", cpu: "abcm", wantErr: true},
		{name: "invalid decimal", cpu: "xyz", wantErr: true},
		{name: "negative", cpu: "-1.5", wantErr: true},
		{name: "zero", cpu: "0", wantErr: true},
		{name: "zero millicpu", cpu: "0m", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCPULimit(tt.cpu)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseCPULimit(%q) = %+v, want error", tt.cpu, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCPULimit(%q) unexpected error: %v", tt.cpu, err)
			}
			if got.Count != tt.wantCount {
				t.Errorf("parseCPULimit(%q).Count = %q, want %q", tt.cpu, got.Count, tt.wantCount)
			}
			if got.Allowance != tt.wantAllowance {
				t.Errorf("parseCPULimit(%q).Allowance = %q, want %q", tt.cpu, got.Allowance, tt.wantAllowance)
			}
		})
	}
}

// TestCPUAllowanceIsAHardQuota is the point of #1034 and the one property
// that must not silently regress: Incus reads a PERCENTAGE allowance as a
// soft scheduler share (cpu.max stays "max", so an idle host lets one tenant
// take all of it) and the ms/ms form as a hard CFS quota. Emitting a
// percentage would look correct in `incus config show` while enforcing
// nothing — exactly the failure #1030 shipped with.
func TestCPUAllowanceIsAHardQuota(t *testing.T) {
	for _, in := range []string{"1", "4", "8", "250m", "1500m", "0.25", "2.5"} {
		cl, err := parseCPULimit(in)
		if err != nil {
			t.Fatalf("parseCPULimit(%q): %v", in, err)
		}
		if strings.HasSuffix(cl.Allowance, "%") {
			t.Errorf("parseCPULimit(%q).Allowance = %q — a percentage is a SOFT share and does not throttle", in, cl.Allowance)
		}
		if !strings.HasSuffix(cl.Allowance, "ms/100ms") {
			t.Errorf("parseCPULimit(%q).Allowance = %q, want the hard-quota form <N>ms/100ms", in, cl.Allowance)
		}
	}
}

// TestCPUQuotaFloor: a request finer than the 1ms quota granularity must
// round up to 1ms, never to 0 — Incus rejects a zero quota, and a container
// that may never be scheduled is never what the caller asked for.
func TestCPUQuotaFloor(t *testing.T) {
	cl, err := parseCPULimit("1m") // 0.001 core → 0.1ms of quota
	if err != nil {
		t.Fatalf("parseCPULimit(\"1m\"): %v", err)
	}
