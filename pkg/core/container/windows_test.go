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
