package audit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// #2773: streaming responses flush after every chunk through
// http.NewResponseController; the status-capturing wrapper (now
// httpx.StatusWriter, tested there) must let that reach the real writer.
// This proves it through the audit middleware itself, on an audited path.
func TestHTTPAuditMiddleware_StreamingHandlerCanFlush(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		for i := 0; i < 3; i++ {
			_, _ = w.Write([]byte("chunk\n"))
			if err := rc.Flush(); err != nil {
				t.Errorf("flush %d: %v", i, err)
				return
			}
		}
