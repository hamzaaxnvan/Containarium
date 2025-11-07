package client

import (
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// A newer daemon may add response fields; an older client must still decode
// the response (#3190). Each body carries an unknown top-level field, and the
// list bodies an unknown nested one.
func TestBoxRunResponsesDiscardUnknownFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"futureField":"x"}`))
			return
		}
		// One body serves every GET: unknown keys are dropped per message.
		_, _ = w.Write([]byte(`{"futureField":"x","runs":[{"futureNested":1}],"inputs":[{"futureNested":1}]}`))
	}))
	defer srv.Close()

	c, err := NewHTTPClient(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}

	t.Run("ListBoxRuns", func(t *testing.T) {
		runs, err := c.ListBoxRuns("alice")
