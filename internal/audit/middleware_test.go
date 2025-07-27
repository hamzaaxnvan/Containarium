package audit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Trenyx audit finding #4 (2026-09-16), HTTP half: HTTPAuditMiddleware had
// no test coverage at all before this. These tests cover both silent-loss
// paths the report named — a Log() error, and a full buffered channel
// dropping an entry before Log() was even attempted — now increment the
// shared persist-failure counter (see metrics_test.go for the equivalent
// gRPC-side coverage).

func waitForPersistFailureCount(t *testing.T, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if GetPersistFailureReport().Count >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("persist-failure count never reached %d; got %d", want, GetPersistFailureReport().Count)
}

func TestHTTPAuditMiddleware_LogErrorRecordsPersistFailure(t *testing.T) {
	ResetPersistFailuresForTest()
	t.Cleanup(ResetPersistFailuresForTest)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := HTTPAuditMiddleware(inner, errAuditLogger{})

	req := httptest.NewRequest(http.MethodPost, "/v1/containers/create", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

