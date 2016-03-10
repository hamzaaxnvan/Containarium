package secrets

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// Phase 4.1 Phase-E — master-key retirement gate.

func TestWithRequireEnvelope_DefaultIsOff(t *testing.T) {
	s := &Store{cipher: newCipher(t)}
	if s.requireEnvelope {
		t.Fatal("default requireEnvelope should be false")
	}
}

func TestWithRequireEnvelope_TogglesField(t *testing.T) {
	s := &Store{cipher: newCipher(t)}
	WithRequireEnvelope(true)(s)
	if !s.requireEnvelope {
		t.Fatal("WithRequireEnvelope(true) didn't set the flag")
	}
	WithRequireEnvelope(false)(s)
	if s.requireEnvelope {
		t.Fatal("WithRequireEnvelope(false) didn't clear the flag")
	}
}

func TestRetirement_LegacyRowRejected(t *testing.T) {
	// Build a legacy row, then read it through a Store
	// with requireEnvelope=true. Must fail with a clear
	// "migrate before retiring" message — the operator
	// flipped the gate too early.
