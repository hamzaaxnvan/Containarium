package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Tests for the TCP/UDP passthrough route methods added for #1550: until
// now AddPassthroughRoute/ListPassthroughRoutes/DeletePassthroughRoute had
// no client-layer caller at all, even though the RPCs and their REST
// mapping (grpc-gateway on /v1/network/passthrough[...]) already existed
// server-side.

func TestHTTPAddPassthroughRoute_Success(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"route": {
				"externalPort": 9443,
				"targetIp": "10.0.3.150",
				"targetPort": 50051,
				"protocol": "ROUTE_PROTOCOL_TCP",
				"active": true,
				"containerName": "alice-container"
			},
			"message": "Passthrough route added: tcp:9443 -> 10.0.3.150:50051 (will sync to iptables)"
		}`))
