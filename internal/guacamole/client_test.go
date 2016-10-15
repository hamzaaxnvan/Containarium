package guacamole

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthenticate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tokens" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		username := r.FormValue("username")
		password := r.FormValue("password")
		if username != "guacadmin" || password != "guacadmin" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		_ = json.NewEncoder(w).Encode(authResponse{
			AuthToken:  "test-token-123",
			Username:   "guacadmin",
			DataSource: "postgresql",
		})
	}))
	defer server.Close()

	client := New(server.URL)

	// Valid credentials
	token, err := client.Authenticate("guacadmin", "guacadmin")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if token != "test-token-123" {
		t.Errorf("Authenticate() token = %q, want %q", token, "test-token-123")
	}

	// Invalid credentials
	_, err = client.Authenticate("wrong", "wrong")
	if err == nil {
		t.Error("Authenticate() with bad credentials should fail")
	}
}

func TestCreateConnection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Verify auth token
		token := r.URL.Query().Get("token")
		if token != "test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		// Decode and verify request body
		var req connectionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if req.Protocol != "rdp" {
			t.Errorf("expected protocol rdp, got %q", req.Protocol)
		}
		if req.Parameters["hostname"] != "10.100.0.50" {
			t.Errorf("expected hostname 10.100.0.50, got %q", req.Parameters["hostname"])
		}
		if req.Parameters["port"] != "3389" {
			t.Errorf("expected port 3389, got %q", req.Parameters["port"])
		}

		_ = json.NewEncoder(w).Encode(connectionResponse{
			Identifier:       "42",
			Name:             req.Name,
			ParentIdentifier: "ROOT",
			Protocol:         "rdp",
		})
	}))
	defer server.Close()

	client := New(server.URL)

	connID, err := client.CreateConnection("test-token", ConnectionConfig{
		Name:     "wintest-container",
		Hostname: "10.100.0.50",
