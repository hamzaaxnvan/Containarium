package gateway

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/events"
)

// #2773 review: HandleSSE used to type-assert w.(http.Flusher), which only
// worked because /v1/events/subscribe is mounted raw and audit-skipped. Under
// the audited chain the wrapper hides the Flusher, so every subscriber would
// have been told "SSE not supported". The handler now flushes through
// http.ResponseController, which follows Unwrap. This mounts it on an
// audited path (not one of audit's skip prefixes) and reads the first event.
func TestHandleSSE_FlushesFirstEventUnderAuditMiddleware(t *testing.T) {
	bus := events.NewBus()
	h := NewEventHandler(bus)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/test/sse", h.HandleSSE)

