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
			_, _ = w.Write([]byte(`{
				"route": {
					"externalPort": 9443,
					"targetIp": "10.0.3.150",
					"targetPort": 50051,
					"protocol": "ROUTE_PROTOCOL_UDP",
					"active": true,
					"containerName": "alice-container"
				},
				"message": "Passthrough route added: udp:9443 -> 10.0.3.150:50051 (will sync to iptables)"
			}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	out, err := handleAddPassthroughRoute(client, map[string]interface{}{
		"external_port":  float64(9443),
		"target_ip":      "10.0.3.150",
		"target_port":    float64(50051),
		"protocol":       "udp",
		"container_name": "alice-container",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, addCalls)
	assert.Contains(t, out, "udp:9443")
	assert.Contains(t, out, "10.0.3.150:50051")
	assert.Contains(t, out, "alice-container")
}

func TestAddPassthroughRoute_DefaultsProtocolToTCP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var req AddPassthroughRouteRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "ROUTE_PROTOCOL_TCP", req.Protocol)
		_, _ = w.Write([]byte(`{"route":{"externalPort":9443,"targetIp":"10.0.3.150","targetPort":50051},"message":"ok"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	_, err := handleAddPassthroughRoute(client, map[string]interface{}{
		"external_port": float64(9443),
		"target_ip":     "10.0.3.150",
		"target_port":   float64(50051),
	})
	require.NoError(t, err)
}

func TestAddPassthroughRoute_RejectsMissingArgsAndBadProtocol(t *testing.T) {
	client := NewClient("http://unused", "token")
	cases := []struct {
		name string
		args map[string]interface{}
