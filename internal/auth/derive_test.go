package auth

import (
	"regexp"
	"strings"
	"testing"
)

const testDeriveSecret = "a-test-jwt-signing-key-of-sufficient-length-0123456789"

func newDeriveManager(t *testing.T, key string) *TokenManager {
	t.Helper()
	tm, err := NewTokenManager(key, "containarium-test")
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
