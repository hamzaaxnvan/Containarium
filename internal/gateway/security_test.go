package gateway

import (
	"net/http/httptest"
	"testing"
)

func TestGetAllowedOrigins_Default(t *testing.T) {
	// Ensure env var is not set
	t.Setenv("CONTAINARIUM_ALLOWED_ORIGINS", "")

	origins := getAllowedOrigins()

	// Should return default localhost origins
	expectedDefaults := []string{
		"http://localhost:3000",
		"http://localhost:8080",
		"http://localhost",
	}

	if len(origins) != len(expectedDefaults) {
		t.Errorf("expected %d default origins, got %d", len(expectedDefaults), len(origins))
	}

	for i, expected := range expectedDefaults {
		if origins[i] != expected {
			t.Errorf("expected origin %s at index %d, got %s", expected, i, origins[i])
		}
	}
}

func TestGetAllowedOrigins_FromEnv(t *testing.T) {
	t.Setenv("CONTAINARIUM_ALLOWED_ORIGINS", "https://example.com,https://app.example.com")

	origins := getAllowedOrigins()

	expected := []string{"https://example.com", "https://app.example.com"}

	if len(origins) != len(expected) {
		t.Errorf("expected %d origins, got %d", len(expected), len(origins))
	}

	for i, exp := range expected {
		if origins[i] != exp {
			t.Errorf("expected origin %s at index %d, got %s", exp, i, origins[i])
		}
	}
}

func TestGetAllowedOrigins_TrimsWhitespace(t *testing.T) {
	t.Setenv("CONTAINARIUM_ALLOWED_ORIGINS", "  https://example.com  ,  https://app.example.com  ")

	origins := getAllowedOrigins()

	expected := []string{"https://example.com", "https://app.example.com"}

	for i, exp := range expected {
		if origins[i] != exp {
			t.Errorf("expected origin %s at index %d, got %s (whitespace not trimmed)", exp, i, origins[i])
		}
	}
}

func TestGetTerminalAllowedOrigins_Default(t *testing.T) {
	t.Setenv("CONTAINARIUM_ALLOWED_ORIGINS", "")

	origins := getTerminalAllowedOrigins()

	// Should return default localhost origins
	if len(origins) < 1 {
		t.Error("expected at least one default origin")
	}

	// All defaults should be localhost
	for _, origin := range origins {
		if origin != "http://localhost:3000" &&
			origin != "http://localhost:8080" &&
			origin != "http://localhost" {
			t.Errorf("unexpected default origin: %s", origin)
		}
	}
}
