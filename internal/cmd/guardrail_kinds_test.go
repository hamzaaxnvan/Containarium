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
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().String(), eng
}

// #2362: the default policy says REDACT SECRET. Against an engine that
// cannot scan secrets, apply must FAIL before writing anything — never
// attest a PASS that only ever looked for PII.
func TestGuardrail_ApplyFailsClosedWhenEngineCannotCoverPolicy(t *testing.T) {
	addr, eng := servePIIOnlyEngine(t)
	work := t.TempDir()
	in := filepath.Join(work, "raw")
	must(t, os.MkdirAll(in, 0o755))
	must(t, os.WriteFile(filepath.Join(in, "a.txt"), []byte("mail ann@example.com\n"), 0o644))
	out := filepath.Join(work, "clean")

	o, err := runGuardrail(t, "apply", in, "--out", out, "--engine", addr)
	if err == nil || !strings.Contains(err.Error(), "FailedPrecondition") || !strings.Contains(err.Error(), "pii,secret") {
		t.Fatalf("apply must fail closed naming the kinds it asked for; got err=%v\n%s", err, o)
	}
	if eng.calls != 1 {
		t.Errorf("engine called %d times; want exactly the first scan, no re-scan", eng.calls)
	}
