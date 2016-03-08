package auth

import (
	"testing"
	"time"
)

func TestNewTokenManager(t *testing.T) {
	tm, _ := NewTokenManager("test-secret-must-be-at-least-32-bytes-long-ok", "test-issuer")

	if tm == nil {
		t.Fatal("NewTokenManager returned nil")
	}

	if tm.maxTokenExpiry != DefaultMaxTokenExpiry {
		t.Errorf("expected maxTokenExpiry to be %v, got %v", DefaultMaxTokenExpiry, tm.maxTokenExpiry)
	}
}

func TestNewTokenManager_WithEnvOverride(t *testing.T) {
	// Set environment variable to 48 hours
	t.Setenv("CONTAINARIUM_MAX_TOKEN_EXPIRY_HOURS", "48")

	tm, _ := NewTokenManager("test-secret-must-be-at-least-32-bytes-long-ok", "test-issuer")

	expected := 48 * time.Hour
	if tm.maxTokenExpiry != expected {
		t.Errorf("expected maxTokenExpiry to be %v, got %v", expected, tm.maxTokenExpiry)
	}
}

func TestGenerateToken_EnforcesMaxExpiry(t *testing.T) {
	tm, _ := NewTokenManager("test-secret-must-be-at-least-32-bytes-long-ok", "test-issuer")

	tests := []struct {
		name            string
		requestedExpiry time.Duration
		expectClamped   bool
	}{
		{
			name:            "zero expiry should be clamped to max",
			requestedExpiry: 0,
			expectClamped:   true,
		},
		{
			name:            "negative expiry should be clamped to max",
			requestedExpiry: -1 * time.Hour,
			expectClamped:   true,
		},
		{
			name:            "expiry exceeding max should be clamped",
			requestedExpiry: 365 * 24 * time.Hour, // 1 year
			expectClamped:   true,
		},
		{
			name:            "valid expiry within max should be allowed",
			requestedExpiry: 24 * time.Hour,
			expectClamped:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokenStr, err := tm.GenerateToken("testuser", []string{"admin"}, tt.requestedExpiry)
			if err != nil {
				t.Fatalf("GenerateToken failed: %v", err)
			}

			// Validate the token and check expiry
			claims, err := tm.ValidateToken(tokenStr)
			if err != nil {
				t.Fatalf("ValidateToken failed: %v", err)
			}

			// Token should always have an expiry (no nil ExpiresAt)
			if claims.ExpiresAt == nil {
				t.Error("SECURITY: Token has no expiry set - this should never happen")
			}

			// Calculate actual expiry duration
			actualExpiry := time.Until(claims.ExpiresAt.Time)

			if tt.expectClamped {
				// Should be close to max expiry (within a few seconds for test execution time)
				expectedMax := tm.maxTokenExpiry
				tolerance := 5 * time.Second
				if actualExpiry > expectedMax+tolerance || actualExpiry < expectedMax-tolerance {
					t.Errorf("expected expiry close to %v, got %v", expectedMax, actualExpiry)
				}
			} else {
				// Should be close to requested expiry
				tolerance := 5 * time.Second
				if actualExpiry > tt.requestedExpiry+tolerance || actualExpiry < tt.requestedExpiry-tolerance {
					t.Errorf("expected expiry close to %v, got %v", tt.requestedExpiry, actualExpiry)
				}
			}
		})
	}
}

func TestGenerateToken_NoNonExpiringTokens(t *testing.T) {
	// This is a critical security test - ensure we never create tokens without expiry
	tm, _ := NewTokenManager("test-secret-must-be-at-least-32-bytes-long-ok", "test-issuer")

	// Try various ways to create a "non-expiring" token
	testCases := []time.Duration{
		0,
		-1,
		-1 * time.Hour,
		-24 * time.Hour,
	}

	for _, expiry := range testCases {
		tokenStr, err := tm.GenerateToken("testuser", []string{"admin"}, expiry)
		if err != nil {
			t.Fatalf("GenerateToken failed for expiry %v: %v", expiry, err)
