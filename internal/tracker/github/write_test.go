package github

import (
	"context"
	"encoding/json"
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
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"user": {"login": "agent"}, "created_at": "2027-01-01T00:00:00Z", "body": "hello"}`))
	}))
	defer srv.Close()

	a := New(nil)
	comment, err := a.Comment(context.Background(), tracker.Conn{BaseURL: srv.URL, Project: "acme/widgets", Credential: "ghp_x"}, 5, "hello")
	if err != nil {
		t.Fatalf("Comment: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/repos/acme/widgets/issues/5/comments" {
		t.Errorf("method=%s path=%s, want POST /repos/acme/widgets/issues/5/comments", gotMethod, gotPath)
	}
	if gotBody["body"] != "hello" {
		t.Errorf("request body = %v, want body=hello", gotBody)
	}
	if comment.Author != "agent" || comment.Body != "hello" {
		t.Errorf("comment = %+v, want author=agent body=hello", comment)
	}
}

func TestAssignIfUnassigned_AssignsWhenUnassigned(t *testing.T) {
	var assignPath string
	var assignBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/acme/widgets/issues/5" && r.Method == http.MethodGet:
