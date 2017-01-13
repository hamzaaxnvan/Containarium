package ratelimit

import (
	"errors"
	"testing"
)

var errBoom = errors.New("boom")

func TestDisabled_AlwaysAllows(t *testing.T) {
	l := Disabled()
	for i := 0; i < 100; i++ {
		if !l.Allow("alice") {
			t.Fatalf("Disabled limiter refused call %d, want always allowed", i)
		}
	}
}

func TestPoisoned_AlwaysDenies(t *testing.T) {
	l := Poisoned(errBoom)
	if l.Allow("alice") {
		t.Fatal("Poisoned limiter allowed a call, want always denied")
	}
	if l.Err() == nil {
		t.Fatal("Err() = nil on a Poisoned limiter, want the configuration error")
	}
}

func TestNew_AllowsUpToBurstThenDenies(t *testing.T) {
	// A slow rate (well under 1/sec) with burst=3 means the 4th call in
	// the same instant must be denied — the token bucket has nothing
	// left and refill is negligible over a test's execution time.
	l := New(0.001, 3)

	for i := 0; i < 3; i++ {
		if !l.Allow("alice") {
			t.Fatalf("call %d within burst was denied, want allowed", i)
		}
	}
	if l.Allow("alice") {
		t.Error("4th call beyond burst was allowed, want denied")
	}
}

func TestNew_PerSubjectBucketsAreIndependent(t *testing.T) {
	l := New(0.001, 1)

	if !l.Allow("alice") {
		t.Fatal("alice's first call was denied, want allowed")
	}
