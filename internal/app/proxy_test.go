package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeCaddy is a minimal stand-in for the Caddy admin API that simulates the
// real semantics: GET on a config sub-path returns that sub-tree, POST /load
// atomically replaces the whole config. This is what the manager's
// getFullConfig + loadConfig path expects, and lets tests catch bugs that
// only show up under real /load semantics (vs. naive PATCH-replace).
type fakeCaddy struct {
	config map[string]interface{}
	loads  int
}

func newFakeCaddy(initial map[string]interface{}) *httptest.Server {
	fc := &fakeCaddy{config: initial}
	mux := http.NewServeMux()
	mux.HandleFunc("/config/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/config/")
		path = strings.TrimSuffix(path, "/")
		if path == "" {
			_ = json.NewEncoder(w).Encode(fc.config)
			return
		}
		var node interface{} = fc.config
		for _, p := range strings.Split(path, "/") {
			m, ok := node.(map[string]interface{})
			if !ok {
				http.Error(w, "null", http.StatusNotFound)
				return
			}
			node = m[p]
		}
		_ = json.NewEncoder(w).Encode(node)
	})
	mux.HandleFunc("/load", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var newCfg map[string]interface{}
		if err := json.Unmarshal(body, &newCfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fc.config = newCfg
		fc.loads++
		w.WriteHeader(http.StatusOK)
	})
	return httptest.NewServer(mux)
}

func TestNewProxyManager(t *testing.T) {
	pm := NewProxyManager("http://localhost:2019", "containarium.dev")

	if pm.caddyAdminURL != "http://localhost:2019" {
		t.Errorf("caddyAdminURL = %q, want %q", pm.caddyAdminURL, "http://localhost:2019")
	}

	if pm.baseDomain != "containarium.dev" {
		t.Errorf("baseDomain = %q, want %q", pm.baseDomain, "containarium.dev")
	}

	if pm.serverName != DefaultCaddyServerName {
		t.Errorf("serverName = %q, want %q", pm.serverName, DefaultCaddyServerName)
	}
}

func TestProxyManager_SetServerName(t *testing.T) {
	pm := NewProxyManager("http://localhost:2019", "containarium.dev")

	pm.SetServerName("custom-server")

	if pm.serverName != "custom-server" {
		t.Errorf("serverName = %q, want %q", pm.serverName, "custom-server")
	}
}

func TestProxyManager_AddRoute(t *testing.T) {
	// Create test server
	var routePath string
	var receivedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle route addition
		if r.URL.Path == "/config/apps/http/servers/srv0/routes" && r.Method == "POST" {
			routePath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&receivedBody)
			w.WriteHeader(http.StatusOK)
			return
		}
		// Handle TLS provisioning requests (just return OK)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	pm := NewProxyManager(server.URL, "containarium.dev")

	err := pm.AddRoute("testuser-myapp", "10.100.0.15", 3000)
	if err != nil {
		t.Fatalf("AddRoute() error = %v", err)
	}

	// Verify the path includes the server name
	expectedPath := "/config/apps/http/servers/srv0/routes"
	if routePath != expectedPath {
		t.Errorf("request path = %q, want %q", routePath, expectedPath)
	}

	// Verify the route ID
	if id, ok := receivedBody["@id"].(string); !ok || id != "testuser-myapp" {
		t.Errorf("route @id = %v, want %q", receivedBody["@id"], "testuser-myapp")
	}

	// Verify host match
	if matches, ok := receivedBody["match"].([]interface{}); ok && len(matches) > 0 {
		match := matches[0].(map[string]interface{})
		if hosts, ok := match["host"].([]interface{}); ok && len(hosts) > 0 {
			if hosts[0] != "testuser-myapp.containarium.dev" {
				t.Errorf("host = %v, want %q", hosts[0], "testuser-myapp.containarium.dev")
			}
		}
	}
}

func TestProxyManager_AddRoute_CustomServerName(t *testing.T) {
	var routePath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture route addition path
		if r.URL.Path == "/config/apps/http/servers/main/routes" && r.Method == "POST" {
			routePath = r.URL.Path
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	pm := NewProxyManager(server.URL, "containarium.dev")
	pm.SetServerName("main")

	err := pm.AddRoute("testuser-myapp", "10.100.0.15", 3000)
	if err != nil {
		t.Fatalf("AddRoute() error = %v", err)
	}

	// Verify the path uses the custom server name
	expectedPath := "/config/apps/http/servers/main/routes"
	if routePath != expectedPath {
		t.Errorf("request path = %q, want %q", routePath, expectedPath)
	}
}

func TestProxyManager_RemoveRoute(t *testing.T) {
	var receivedPath string
	var receivedMethod string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	pm := NewProxyManager(server.URL, "containarium.dev")

	err := pm.RemoveRoute("testuser-myapp")
	if err != nil {
		t.Fatalf("RemoveRoute() error = %v", err)
	}

	if receivedMethod != "DELETE" {
		t.Errorf("method = %q, want %q", receivedMethod, "DELETE")
	}

	expectedPath := "/id/testuser-myapp"
	if receivedPath != expectedPath {
		t.Errorf("request path = %q, want %q", receivedPath, expectedPath)
	}
}

func TestProxyManager_RemoveRoute_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// For DELETE by ID, return 404
		if r.Method == "DELETE" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// For GET routes (fallback), return empty list
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	pm := NewProxyManager(server.URL, "containarium.dev")

	// Should not return error when route doesn't exist
	err := pm.RemoveRoute("nonexistent")
	if err != nil {
		t.Errorf("RemoveRoute() should not error when route not found, got: %v", err)
	}
}

func TestProxyManager_AddRoute_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	}))
	defer server.Close()

	pm := NewProxyManager(server.URL, "containarium.dev")

	err := pm.AddRoute("testuser-myapp", "10.100.0.15", 3000)
	if err == nil {
		t.Error("AddRoute() expected error for 500 response")
	}
}

func TestProxyManager_ListRoutes(t *testing.T) {
	var receivedPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path

		routes := []caddyRouteJSON{
			{
				ID: "testuser-app1",
				Match: []CaddyMatchTyped{
					{Host: []string{"testuser-app1.containarium.dev"}},
				},
				Handle: []CaddyHandler{
					CaddyReverseProxyHandler{
						Handler: "reverse_proxy",
						Upstreams: []CaddyUpstreamTyped{
							{Dial: "10.100.0.15:3000"},
						},
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(routes)
	}))
	defer server.Close()

	pm := NewProxyManager(server.URL, "containarium.dev")

	routes, err := pm.ListRoutes()
	if err != nil {
		t.Fatalf("ListRoutes() error = %v", err)
	}

	// Verify the path includes the server name
	expectedPath := "/config/apps/http/servers/srv0/routes"
	if receivedPath != expectedPath {
		t.Errorf("request path = %q, want %q", receivedPath, expectedPath)
	}

	if len(routes) != 1 {
		t.Fatalf("ListRoutes() returned %d routes, want 1", len(routes))
	}

	if routes[0].Subdomain != "testuser-app1" {
		t.Errorf("route subdomain = %q, want %q", routes[0].Subdomain, "testuser-app1")
	}

	if routes[0].FullDomain != "testuser-app1.containarium.dev" {
		t.Errorf("route full domain = %q, want %q", routes[0].FullDomain, "testuser-app1.containarium.dev")
	}
}

func TestDefaultCaddyServerName(t *testing.T) {
	if DefaultCaddyServerName != "srv0" {
		t.Errorf("DefaultCaddyServerName = %q, want %q", DefaultCaddyServerName, "srv0")
	}
}

// Tests for new route management functionality

func TestProxyManager_RemoveRoute_FullDomain(t *testing.T) {
	// Test that RemoveRoute correctly extracts subdomain from full domain
	var receivedPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
