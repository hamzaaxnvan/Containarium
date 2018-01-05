package container

import (
	"strings"
	"testing"
)

// Phase 2.5 follow-up — OTel bearer header stamping.

func TestBuildOTelEnvMap_NoBearerOmitsHeader(t *testing.T) {
	m := buildOTelEnvMap("alice", "alice-container", "backend-1", "http://10.0.3.5:4318", "")
	if _, ok := m["OTEL_EXPORTER_OTLP_HEADERS"]; ok {
		t.Fatal("empty bearer should NOT emit OTEL_EXPORTER_OTLP_HEADERS")
	}
}

func TestBuildOTelEnvMap_BearerSetsAuthorizationHeader(t *testing.T) {
	m := buildOTelEnvMap("alice", "alice-container", "backend-1", "http://10.0.3.5:4318", "secret-xyz")
	got, ok := m["OTEL_EXPORTER_OTLP_HEADERS"]
	if !ok {
		t.Fatal("bearer present but header missing")
	}
	want := "Authorization=Bearer secret-xyz"
	if got != want {
		t.Fatalf("OTEL_EXPORTER_OTLP_HEADERS = %q; want %q", got, want)
	}
}

func TestOTelEnvVarsForMigration_NoBearerKeepsBackwardsCompat(t *testing.T) {
	// The pre-2.5 signature wraps the new bearer-aware one
	// with bearer="". Existing callers (migration-adopt
	// path) keep working unchanged.
	m := OTelEnvVarsForMigration("alice", "alice-container", "backend-1", "http://x:4318")
	if _, ok := m["OTEL_EXPORTER_OTLP_HEADERS"]; ok {
		t.Fatal("legacy signature must not stamp the header")
	}
	if m["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://x:4318" {
		t.Fatalf("endpoint missing or wrong: %v", m)
	}
}

func TestOTelEnvVarsForMigrationWithBearer_EmptyEndpointReturnsNil(t *testing.T) {
	// Empty endpoint is the "collector not configured"
	// signal — return nil so the caller doesn't stamp a
	// dead endpoint OR an authorization header.
	m := OTelEnvVarsForMigrationWithBearer("alice", "alice-container", "backend-1", "", "secret")
	if m != nil {
		t.Fatalf("empty endpoint should return nil; got %v", m)
