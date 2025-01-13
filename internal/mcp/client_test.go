package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewClient tests client creation
func TestNewClient(t *testing.T) {
	client := NewClient("http://localhost:8080", "test-token")

	assert.NotNil(t, client)
	assert.Equal(t, "http://localhost:8080", client.baseURL)
	assert.Equal(t, "test-token", client.jwtToken)
	assert.NotNil(t, client.httpClient)
}

// TestClientDoRequest tests HTTP request handling
func TestClientDoRequest(t *testing.T) {
	tests := []struct {
		name           string
		serverResponse string
		serverStatus   int
		expectError    bool
	}{
		{
			name:           "successful request",
			serverResponse: `{"success": true}`,
			serverStatus:   http.StatusOK,
			expectError:    false,
		},
		{
			name:           "server error",
			serverResponse: `{"error": "something went wrong"}`,
			serverStatus:   http.StatusInternalServerError,
			expectError:    true,
		},
		{
			name:           "unauthorized",
			serverResponse: `{"error": "unauthorized"}`,
			serverStatus:   http.StatusUnauthorized,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify auth header
				auth := r.Header.Get("Authorization")
				assert.Equal(t, "Bearer test-token", auth)

				// Verify content type
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

				// Verify the client advertises its version: a conventional
				// User-Agent ("containarium/<ver>") plus the explicit header.
				assert.True(t, strings.HasPrefix(r.Header.Get("User-Agent"), "containarium/"),
					"User-Agent must be containarium/<version>, got %q", r.Header.Get("User-Agent"))
				assert.Equal(t, version.GetVersion(), r.Header.Get(version.ClientVersionHeader))

				w.WriteHeader(tt.serverStatus)
				w.Write([]byte(tt.serverResponse))
			}))
			defer server.Close()

			// Create client
			client := NewClient(server.URL, "test-token")

			// Make request
			resp, err := client.doRequest("GET", "/test", nil)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, resp)
			}
		})
	}
}

// TestClientListContainers tests list containers API call
func TestClientListContainers(t *testing.T) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/v1/containers", r.URL.Path)

		resp := ListContainersResponse{
			Containers: []Container{
				{
					Name:     "alice-container",
					Username: "alice",
					State:    "Running",
				},
			},
			TotalCount: 1,
		}

		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create client and call
	client := NewClient(server.URL, "test-token")
	resp, err := client.ListContainers()

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, 1, resp.TotalCount)
	assert.Len(t, resp.Containers, 1)
	assert.Equal(t, "alice-container", resp.Containers[0].Name)
}

// TestClientListRoutes verifies the new list_routes tool's wire path:
// correct method + path, query-string filter encoding, and that the
// daemon's proto-shaped response parses into ProxyRoute.
func TestClientListRoutes(t *testing.T) {
	t.Run("no filters: bare GET /v1/network/routes", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "GET", r.Method)
			assert.Equal(t, "/v1/network/routes", r.URL.Path)
			assert.Empty(t, r.URL.RawQuery, "no filters → no query string")
			_ = json.NewEncoder(w).Encode(ListRoutesResponse{
				Routes: []ProxyRoute{
					{
						Subdomain:   "alice-web",
						FullDomain:  "alice-web.example.com",
						ContainerIP: "10.0.3.42",
						Port:        80,
						Active:      true,
						AppName:     "alice-container",
					},
				},
				TotalCount: 1,
			})
		}))
		defer srv.Close()

		c := NewClient(srv.URL, "t")
		resp, err := c.ListRoutes("", false)
		require.NoError(t, err)
		require.Len(t, resp.Routes, 1)
		assert.Equal(t, "alice-web.example.com", resp.Routes[0].FullDomain)
		assert.True(t, resp.Routes[0].Active)
	})

	t.Run("with filters: both query params encoded", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "alice", r.URL.Query().Get("username"))
			assert.Equal(t, "true", r.URL.Query().Get("activeOnly"))
			_ = json.NewEncoder(w).Encode(ListRoutesResponse{Routes: nil, TotalCount: 0})
		}))
		defer srv.Close()

		c := NewClient(srv.URL, "t")
		_, err := c.ListRoutes("alice", true)
		require.NoError(t, err)
	})
}

// TestClientGetContainer tests get container API call
func TestClientGetContainer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/v1/containers/alice", r.URL.Path)

		resp := GetContainerResponse{
			Container: Container{
				Name:     "alice-container",
				Username: "alice",
				State:    "Running",
			},
		}

		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	resp, err := client.GetContainer("alice")

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "alice-container", resp.Container.Name)
	assert.Equal(t, "alice", resp.Container.Username)
}

// TestClientCreateContainer tests create container API call
func TestClientCreateContainer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/v1/containers", r.URL.Path)

		// Decode request body
		var req CreateContainerRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)

		assert.Equal(t, "alice", req.Username)
		assert.Equal(t, "4", req.Resources.CPU)
		assert.Equal(t, "8GB", req.Resources.Memory)

		resp := CreateContainerResponse{
			Container: Container{
				Name:     "alice-container",
				Username: "alice",
				State:    "Running",
			},
			Message: "Container created",
		}

		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	req := CreateContainerRequest{
		Username: "alice",
		Resources: &ResourceLimits{
			CPU:    "4",
			Memory: "8GB",
			Disk:   "50GB",
		},
	}

	resp, err := client.CreateContainer(req)

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "alice-container", resp.Container.Name)
	assert.Equal(t, "Container created", resp.Message)
}

// TestClientDeleteContainer tests delete container API call
func TestClientDeleteContainer(t *testing.T) {
	tests := []struct {
		name  string
		force bool
	}{
		{"normal delete", false},
		{"force delete", true},
