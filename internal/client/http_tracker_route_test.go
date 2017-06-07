package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Scope-route REST calls (#2021) — method + path must match the
// google.api.http mapping on TrackerService in tracker.proto.

func TestTrackerRoutes_HTTPPathsAndDecoding(t *testing.T) {
	var gotMethod, gotPath string
	var respBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.EscapedPath()
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respBody)
	}))
	defer srv.Close()

	c, err := NewHTTPClient(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
	}

	respBody = `{"message":"route created","route":{"username":"alice","connection":"a/b","scope":"product","skillId":"product-define"}}`
	route, msg, err := c.SetTrackerRoute(&pb.SetTrackerRouteRequest{Username: "alice", Connection: "a/b", Scope: "product", SkillId: "product-define"})
	if err != nil {
