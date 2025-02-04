package secrets

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"testing"
)

// Phase 4.1 Phase-A — InProcKMS wrap/unwrap symmetry +
// edge-case rejections.

func makeMasterKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, MasterKeySize)
	if _, err := io.ReadFull(rand.Reader, k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return k
}

func TestInProcKMS_WrapUnwrapSymmetry(t *testing.T) {
	mk := makeMasterKey(t)
	kms, err := NewInProcKMS(mk)
	if err != nil {
		t.Fatalf("NewInProcKMS: %v", err)
	}
