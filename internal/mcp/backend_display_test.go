package mcp

// Asserts create/list/delete container output surfaces the RESOLVED backend/pool
// — so a placement that silently fell back to the default (cloud #686) is
// visible at the tool layer instead of via a later egress test.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListContainers_ShowsBackendAndPool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"containers":[{"name":"box1","username":"cld-1","state":"running","backendId":"tunnel-gpu-node-a","pool":"gpu-pool"}],"totalCount":1}`))
	}))
	defer server.Close()

	out, err := handleListContainers(NewClient(server.URL, "t"), map[string]interface{}{})
	require.NoError(t, err)
	assert.Contains(t, out, "Backend: tunnel-gpu-node-a")
	assert.Contains(t, out, "Pool: gpu-pool")
}

func TestCreateContainer_ShowsBackend_AndFallbackWarning(t *testing.T) {
	// Server reports the box landed on the default GCP backend, regardless of
	// the requested backend_id — the silent-fallback case.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"container":{"name":"box1","username":"cld-1","state":"running","backendId":"cloud-backend-spot"},"message":"created"}`))
	}))
	defer server.Close()

	out, err := handleCreateContainer(NewClient(server.URL, "t"), map[string]interface{}{
		"username":   "cld-1",
