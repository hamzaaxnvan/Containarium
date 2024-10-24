package containariumotel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newOTLPSink returns an httptest.Server that accepts OTLP/HTTP POSTs
// with a 200 OK. Used in tests where we want Init to succeed without
// pointing at the actual collector. Cleaned up via t.Cleanup.
func newOTLPSink(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestInit_FailOpenWithoutEndpoint(t *testing.T) {
	_resetForTests()
	clearEnv(t)
	shutdown, err := Init(context.Background())
	if err != nil {
		t.Fatalf("Init returned error in fail-open path: %v", err)
	}
	if shutdown == nil {
		t.Fatal("shutdown was nil; expected noopShutdown")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("no-op shutdown returned error: %v", err)
	}
}

func TestInit_WithEndpoint(t *testing.T) {
	_resetForTests()
	clearEnv(t)
	srv := newOTLPSink(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", srv.URL)
	t.Setenv("OTEL_SERVICE_NAME", "test-svc")
	t.Setenv("CONTAINARIUM_CONTAINER_ID", "alice-container")
	t.Setenv("CONTAINARIUM_BACKEND_ID", "node-7")
	t.Setenv("CONTAINARIUM_TENANT_ID", "alice")

	shutdown, err := Init(context.Background())
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if shutdown == nil {
		t.Fatal("shutdown nil")
	}
	if err := shutdown(context.Background()); err != nil {
