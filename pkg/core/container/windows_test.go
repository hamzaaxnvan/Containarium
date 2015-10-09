package container

import (
	"testing"
)

func TestGeneratePassword(t *testing.T) {
	// Test basic generation
	pw1, err := generatePassword(16)
	if err != nil {
		t.Fatalf("generatePassword(16) error = %v", err)
	}
	// 16 bytes = 32 hex chars
	if len(pw1) != 32 {
		t.Errorf("generatePassword(16) length = %d, want 32", len(pw1))
	}

	// Test uniqueness — two calls should produce different passwords
	pw2, err := generatePassword(16)
	if err != nil {
		t.Fatalf("generatePassword(16) error = %v", err)
	}
	if pw1 == pw2 {
		t.Error("generatePassword() produced identical passwords on two calls")
	}

	// Test different sizes
	sizes := []struct {
		byteLen   int
		wantChars int
	}{
		{8, 16},
		{16, 32},
		{32, 64},
	}
	for _, tt := range sizes {
		pw, err := generatePassword(tt.byteLen)
		if err != nil {
			t.Fatalf("generatePassword(%d) error = %v", tt.byteLen, err)
		}
		if len(pw) != tt.wantChars {
			t.Errorf("generatePassword(%d) length = %d, want %d", tt.byteLen, len(pw), tt.wantChars)
