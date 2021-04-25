package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestHTTPDeployRecipe_SendsGuardrailInput: the REST body carries
// guardrail_input in a form the grpc-gateway (protojson) decodes back to
// the same message, and an ungated deploy sends none.
func TestHTTPDeployRecipe_SendsGuardrailInput(t *testing.T) {
	var bodies []*pb.DeployRecipeRequest
