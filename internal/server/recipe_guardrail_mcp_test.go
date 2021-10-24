package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/mcp"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
)

// Run the real stdio MCP server in a helper process, through the generated
// REST gateway and daemon gate, against the fake container backend.
func TestDeployGate_MCP(t *testing.T) {
	for _, withInput := range []bool{true, false} {
		t.Run(map[bool]string{true: "valid input", false: "missing input"}[withInput], func(t *testing.T) {
			f := newGateFixture(t)
			f.srv.guardrailPolicy = fakePolicyProvider{policy: f.policy}
			mux := runtime.NewServeMux(runtime.WithMetadata(func(context.Context, *http.Request) metadata.MD {
				md, _ := metadata.FromIncomingContext(tenantWithScopes("alice", auth.ScopeContainersWrite))
				return md
			}))
			require.NoError(t, pb.RegisterRecipeServiceHandlerServer(context.Background(), mux, f.srv))
			srv := httptest.NewServer(mux)
			defer srv.Close()
			args := map[string]interface{}{"recipe_id": "gated", "name": "alice"}
			if withInput {
				raw, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(f.req.GuardrailInput)
				require.NoError(t, err)
				args["guardrail_input"] = json.RawMessage(raw)
			}
			request, err := json.Marshal(mcp.MCPRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: map[string]interface{}{
				"name": "deploy_recipe", "arguments": args,
			}})
