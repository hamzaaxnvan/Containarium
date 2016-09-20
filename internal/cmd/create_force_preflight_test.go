package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeForceDaemon is a daemon with one existing box ("alice") that records the
// order of what the CLI does to it. rejectDryRun makes the validate-only
// create fail the way a real daemon fails an unknown stack.
type fakeForceDaemon struct {
	mu           sync.Mutex
	calls        []string
	rejectDryRun bool
	srv          *httptest.Server
}

func newFakeForceDaemon(t *testing.T) *fakeForceDaemon {
	t.Helper()
	d := &fakeForceDaemon{}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/containers/alice":
			d.calls = append(d.calls, "GET")
			_, _ = w.Write([]byte(`{"container":{"name":"alice-container","username":"alice","state":"CONTAINER_STATE_RUNNING"}}`))
