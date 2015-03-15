package ipam

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNew_RejectsInvalidRanges(t *testing.T) {
	cases := []struct {
		name             string
		start, end, host string
		quarantine       time.Duration
		wantErrSubstring string
	}{
		{"start after end", "10.0.0.20", "10.0.0.10", "10.0.0.1", 0, "after"},
		{"malformed start", "not-an-ip", "10.0.0.10", "10.0.0.1", 0, "start"},
		{"malformed end", "10.0.0.10", "not-an-ip", "10.0.0.1", 0, "end"},
		{"malformed host", "10.0.0.10", "10.0.0.20", "not-an-ip", 0, "host"},
		{"host inside range", "10.0.0.10", "10.0.0.20", "10.0.0.15", 0, "overlaps"},
		{"host equals start", "10.0.0.10", "10.0.0.20", "10.0.0.10", 0, "overlaps"},
		{"host equals end", "10.0.0.10", "10.0.0.20", "10.0.0.20", 0, "overlaps"},
		{"negative quarantine", "10.0.0.10", "10.0.0.20", "10.0.0.1", -time.Second, "negative"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(c.start, c.end, c.host, c.quarantine)
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), c.wantErrSubstring) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), c.wantErrSubstring)
			}
		})
	}
}

func TestNew_AcceptsHostOutsideRange(t *testing.T) {
	if _, err := New("10.0.0.10", "10.0.0.20", "10.0.0.1", 0); err != nil {
		t.Fatalf("New: %v", err)
	}
}

func TestAllocate_UniqueAcrossConcurrentCallers(t *testing.T) {
	a, err := New("10.0.0.1", "10.0.0.100", "10.0.0.254", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const n = 100 // exactly the range size — every address must be claimed exactly once
	var wg sync.WaitGroup
	ips := make([]net.IP, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ips[i], errs[i] = a.Allocate()
		}(i)
	}
	wg.Wait()

	seen := make(map[string]int, n)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Allocate %d: %v", i, err)
		}
		seen[ips[i].String()]++
	}
	if len(seen) != n {
		t.Fatalf("got %d distinct addresses, want %d", len(seen), n)
	}
	for ip, count := range seen {
		if count != 1 {
			t.Errorf("address %s allocated %d times", ip, count)
		}
	}
}

func TestAllocate_ExhaustedRangeReturnsTypedError(t *testing.T) {
	a, err := New("10.0.0.1", "10.0.0.2", "10.0.0.254", 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := a.Allocate(); err != nil {
		t.Fatalf("Allocate 1: %v", err)
