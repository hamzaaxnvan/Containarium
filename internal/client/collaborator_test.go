package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHTTPClient_AddCollaborator_Success pins #1785: the client sends the
// owner/collaborator usernames, keys, and grant flags to the REST endpoint
// the daemon's ContainerServer.AddCollaborator exposes, and parses the
// collaborator + ssh_command back out of a grpc-gateway-shaped response.
func TestHTTPClient_AddCollaborator_Success(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"message": "Collaborator bob added to alice-container",
			"collaborator": {
				"ownerUsername": "alice",
				"collaboratorUsername": "bob",
				"containerName": "alice-container",
				"accountName": "alice-container-bob",
				"hasSudo": true
			},
			"sshCommand": "ssh -J jump alice-container-bob@jump"
		}`))
	}))
	defer srv.Close()

	c, err := NewHTTPClient(srv.URL, "tok")
	if err != nil {
		t.Fatalf("NewHTTPClient: %v", err)
