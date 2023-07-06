package mcp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stateServer serves GET /v1/containers/<box> returning a sequence of
// states (the last one repeats once exhausted), so a test can model a box
// transitioning CREATING → RUNNING across polls.
func stateServer(t *testing.T, states []string, ip string) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/containers/") {
			http.NotFound(w, r)
			return
		}
		n := atomic.AddInt32(&calls, 1)
		idx := int(n) - 1
		if idx >= len(states) {
			idx = len(states) - 1
		}
		state := states[idx]
