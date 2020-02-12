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
		case r.Method == http.MethodPost && r.URL.Path == "/v1/containers":
			raw, _ := io.ReadAll(r.Body)
			var body struct {
				DryRun bool `json:"dryRun"`
			}
			_ = json.Unmarshal(raw, &body)
			if !body.DryRun {
				d.calls = append(d.calls, "CREATE")
				_, _ = w.Write([]byte(`{"container":{"name":"alice-container","username":"alice","state":"CONTAINER_STATE_CREATING"}}`))
				return
			}
			d.calls = append(d.calls, "DRYRUN")
			if d.rejectDryRun {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"unknown stack \"typo\"; valid stacks: nodejs"}`))
				return
			}
			_, _ = w.Write([]byte(`{"dryRun":true,"message":"Dry run: accepted. Nothing was created."}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/containers/alice":
			d.calls = append(d.calls, "DELETE")
			_, _ = w.Write([]byte(`{}`))
		default:
			d.calls = append(d.calls, "UNEXPECTED "+r.Method+" "+r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *fakeForceDaemon) sequence() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.Join(d.calls, ",")
