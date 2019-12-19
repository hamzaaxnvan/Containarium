package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// ModelGatewayService REST calls (#1726) — method + path must match the
// google.api.http mapping on ModelGatewayService in model_gateway.proto. This is
// the proto-drift pin for this service: change a path in the proto without
// changing it here (or vice versa) and these assertions fail, instead of the
// mismatch surfacing as a 404 at runtime.
//
// The paths asserted below are, verbatim from the proto:
//
//	PUT    /v1/model-gateway/keys/{key_owner}/{provider}
//	DELETE /v1/model-gateway/keys/{key_owner}/{provider}
//	GET    /v1/model-gateway/keys/{key_owner}/{provider}
//	POST   /v1/model-gateway/tokens
//	GET    /v1/model-gateway/{provider}/models
func TestModelGateway_HTTPPathsAndDecoding(t *testing.T) {
	var gotMethod, gotPath, gotQuery, gotBody string
	var respBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.EscapedPath(), r.URL.RawQuery
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respBody)
	}))
	defer srv.Close()

	c, err := NewHTTPClient(srv.URL, "tok")
	if err != nil {
