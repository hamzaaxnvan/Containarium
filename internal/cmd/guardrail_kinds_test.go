package cmd

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/guardrail"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// piiOnlyEngine is a GuardrailEngineService that, like a real detector
// without a secrets layer, refuses a request naming SECRET with
// FAILED_PRECONDITION and otherwise defers to the reference rules engine.
// Served over real gRPC on loopback so the CLI's --engine path is the one
// under test.
type piiOnlyEngine struct {
	pb.UnimplementedGuardrailEngineServiceServer
	calls int
}

func (e *piiOnlyEngine) Scan(ctx context.Context, req *pb.GuardrailScanRequest) (*pb.GuardrailScanResponse, error) {
	e.calls++
	for _, k := range req.GetKinds() {
		if k == pb.GuardrailKind_GUARDRAIL_KIND_SECRET {
			return nil, status.Error(codes.FailedPrecondition, "category SECRET is not enabled on this server; enabled: [PII]")
		}
	}
	return guardrail.RulesEngine{}.Scan(ctx, req)
}

func servePIIOnlyEngine(t *testing.T) (addr string, eng *piiOnlyEngine) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	eng = &piiOnlyEngine{}
	srv := grpc.NewServer()
	pb.RegisterGuardrailEngineServiceServer(srv, eng)
