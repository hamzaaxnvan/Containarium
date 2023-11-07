package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/footprintai/containarium/internal/tracker"
)

func TestComment_PostsAndNormalizes(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.EscapedPath()
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"author": {"username": "agent"}, "created_at": "2027-01-01T00:00:00Z", "body": "hello"}`))
	}))
	defer srv.Close()

	a := New(nil)
	comment, err := a.Comment(context.Background(), tracker.Conn{BaseURL: srv.URL, Project: "acme/widgets", Credential: "glpat-x"}, 5, "hello")
	if err != nil {
		t.Fatalf("Comment: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v4/projects/acme%2Fwidgets/issues/5/notes" {
		t.Errorf("method=%s path=%s, want POST /api/v4/projects/acme%%2Fwidgets/issues/5/notes", gotMethod, gotPath)
	}
	if gotBody["body"] != "hello" {
		t.Errorf("request body = %v, want body=hello", gotBody)
	}
	if comment.Author != "agent" || comment.Body != "hello" {
		t.Errorf("comment = %+v, want author=agent body=hello", comment)
	}
}

func TestAssignIfUnassigned_AssignsWhenUnassigned(t *testing.T) {
	var putBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v4/projects/acme%2Fwidgets/issues/5":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"iid": 5, "title": "t", "state": "opened", "labels": [], "assignee": null}`))
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/api/v4/user":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": 42, "username": "the-bot", "bot": true}`))
		case r.Method == http.MethodPut:
			_ = json.NewDecoder(r.Body).Decode(&putBody)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.EscapedPath())
		}
	}))
	defer srv.Close()
