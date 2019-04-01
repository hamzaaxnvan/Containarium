package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/guardrailpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// guardrail_policy_get (#2368) is a thin wrapper: it reads the same REST
// endpoint the CLI's HTTP path reads and prints the same text the CLI prints.
func TestGuardrailPolicyGetTool(t *testing.T) {
	resp := &pb.GetGuardrailPolicyResponse{
		Configured: true,
		PolicyHash: "abc123",
		Policy: &pb.ServerGuardrailPolicy{
			Revision: 4,
			Policy: &pb.GuardrailPolicy{Rules: []*pb.GuardrailRule{
				{Kind: pb.GuardrailKind_GUARDRAIL_KIND_SECRET, Action: pb.GuardrailAction_GUARDRAIL_ACTION_BLOCK},
