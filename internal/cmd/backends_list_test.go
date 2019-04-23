package cmd

import (
	"encoding/json"
	"testing"
)

// An unmeasured host must never render as an unloaded one — that
// indistinguishability is what made capacity decisions blind (cloud #966).
func TestFormatCPULoad(t *testing.T) {
	cases := []struct {
		name string
		load *hostLoad
		want string
	}{
		{"no sample", nil, "-"},
		{"load against cores", &hostLoad{CPULoad1m: 6.5, CPUCores: 8}, "6.50/8"},
		{"idle host still reports", &hostLoad{CPULoad1m: 0, CPUCores: 16}, "0.00/16"},
		{"core count unknown", &hostLoad{CPULoad1m: 2.25}, "2.25"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatCPULoad(tc.load); got != tc.want {
				t.Fatalf("formatCPULoad = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatUsedPercent(t *testing.T) {
	full := &hostLoad{
		MemoryUsedBytes:  24 << 30,
		MemoryTotalBytes: 32 << 30,
		DiskUsedBytes:    500 << 30,
		DiskTotalBytes:   1000 << 30,
	}
	cases := []struct {
		name string
		load *hostLoad
		kind usageKind
		want string
	}{
		{"no sample, memory", nil, memoryUsage, "-"},
		{"no sample, disk", nil, diskUsage, "-"},
		{"memory", full, memoryUsage, "75%"},
		{"disk", full, diskUsage, "50%"},
		{"zero total is not a divide by zero", &hostLoad{MemoryUsedBytes: 5}, memoryUsage, "-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatUsedPercent(tc.load, tc.kind); got != tc.want {
				t.Fatalf("formatUsedPercent = %q, want %q", got, tc.want)
			}
		})
	}
}

// grpc-gateway serializes 64-bit integers as JSON strings. If the CLI struct
// tags don't say `,string` the whole backend list fails to decode, so this
// pins the wire shape the daemon actually emits.
func TestBackendInfo_DecodesHostLoadWireShape(t *testing.T) {
	body := []byte(`{"backends":[{
		"id":"tunnel-byoc",
		"type":"tunnel",
		"healthy":true,
		"containerCount":3,
		"hostLoad":{
			"cpuLoad1m":6.5,
			"cpuLoad5m":5,
			"cpuLoad15m":4,
			"cpuCores":8,
			"memoryUsedBytes":"25769803776",
			"memoryTotalBytes":"34359738368",
			"diskUsedBytes":"536870912000",
			"diskTotalBytes":"1073741824000",
			"sampledAt":"2026-07-25T10:30:00Z"
		}
	}]}`)

	var parsed backendsListResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(parsed.Backends) != 1 {
		t.Fatalf("backends = %d, want 1", len(parsed.Backends))
	}
	l := parsed.Backends[0].HostLoad
	if l == nil {
