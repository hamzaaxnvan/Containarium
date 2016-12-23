package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// create_container advertises `os_type` in its schema: `os_type: "rocky9"`
// must reach the daemon as the proto enum name `OS_TYPE_ROCKY_9` (what
// grpc-gateway's protojson accepts), and nothing is sent when the
// argument is absent (#2208).
func TestCreateContainer_OSTypePassthrough(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		_, _ = w.Write([]byte(`{"container":{"name":"box1","username":"cld-1","state":"running"},"message":"created"}`))
	}))
	defer server.Close()

	_, err := handleCreateContainer(NewClient(server.URL, "t"), map[string]interface{}{
