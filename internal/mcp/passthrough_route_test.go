package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for the passthrough-route MCP tools added for #1550: until now
// AddPassthroughRoute/ListPassthroughRoutes/DeletePassthroughRoute had no
// MCP surface, even though the RPCs and their grpc-gateway REST mapping
// already existed server-side (list_routes's own docstring flagged the
// gap).

func TestPassthroughProtocolToProto(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "ROUTE_PROTOCOL_TCP", false},
		{"tcp", "ROUTE_PROTOCOL_TCP", false},
		{"TCP", "ROUTE_PROTOCOL_TCP", false},
		{"udp", "ROUTE_PROTOCOL_UDP", false},
		{" udp ", "ROUTE_PROTOCOL_UDP", false},
		{"sctp", "", true},
	}
	for _, tc := range cases {
		got, err := passthroughProtocolToProto(tc.in)
		if tc.wantErr {
			assert.Error(t, err, "input %q", tc.in)
			continue
		}
		require.NoError(t, err, "input %q", tc.in)
		assert.Equal(t, tc.want, got, "input %q", tc.in)
	}
}

func TestAddPassthroughRoute_HappyPath(t *testing.T) {
	var addCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/network/passthrough":
			addCalls++
			var req AddPassthroughRouteRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			assert.Equal(t, int32(9443), req.ExternalPort)
			assert.Equal(t, "10.0.3.150", req.TargetIP)
			assert.Equal(t, int32(50051), req.TargetPort)
			assert.Equal(t, "ROUTE_PROTOCOL_UDP", req.Protocol)
			assert.Equal(t, "alice-container", req.ContainerName)
