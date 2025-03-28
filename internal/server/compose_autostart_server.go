package server

import (
	"context"
	"encoding/json"
	"github.com/footprintai/containarium/internal/auth"
	"math"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// ComposeAutostartServer implements ComposeAutostartService — the
// outside-the-LXC entry point for the compose-autostart ops that
// already live as MCP tools + a CLI dispatch inside agent-box.
//
// Each handler:
//  1. validates the request
//  2. SSH-execs `agent-box compose <verb> [flags]` into the tenant's
//     LXC via the IncusExecer seam
//  3. parses the uniform JSON envelope agent-box prints to stdout
//     ({"ok":bool,"result":...} or {"ok":false,"error":"..."})
//  4. maps the envelope onto the typed proto response or a gRPC
//     status error
//
// The IncusExecer interface is the testability seam — production code
// wires `pkg/core/incus.Client` (which implements ExecWithOutput);
// unit tests pass a fake that returns canned stdout/stderr/error.
//
// Tenancy: every RPC takes username + execs into "<username>-container"
// (the convention the rest of the daemon uses; see container.Manager).
// Auth on these endpoints is delegated to the gRPC interceptors that
// wrap the daemon's services — same posture as ContainerService.
type ComposeAutostartServer struct {
	pb.UnimplementedComposeAutostartServiceServer

	incus IncusExecer
}

// IncusExecer is the narrow seam this handler needs from the Incus
// client. Matches the signature of *incus.Client.ExecWithOutput.
type IncusExecer interface {
	ExecWithOutput(containerName string, command []string) (stdout, stderr string, err error)
}

func NewComposeAutostartServer(execer IncusExecer) *ComposeAutostartServer {
	return &ComposeAutostartServer{incus: execer}
}

// composeEnvelope is the wire format agent-box's CLI emits on stdout
// (see internal/agentbox/compose_cli.go writeOK / writeErr). Result is
// json.RawMessage so handlers can unmarshal into per-verb shapes
// without re-encoding through map[string]any.
type composeEnvelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// execAgentBoxCompose runs `agent-box compose <verb> args...` in the
// tenant's LXC, parses the envelope, and returns the typed Result
// payload OR a ready-to-return gRPC status error.
//
// Errors map:
//   - transport (exec failure, LXC missing) → codes.Internal
//   - envelope.OK == false                  → codes.FailedPrecondition with the daemon's error
//   - malformed envelope                    → codes.Internal
func (s *ComposeAutostartServer) execAgentBoxCompose(username, verb string, args []string) (json.RawMessage, error) {
	if username == "" {
		return nil, status.Error(codes.InvalidArgument, "username is required")
	}
	containerName := username + "-container"
	cmd := append([]string{"agent-box", "compose", verb}, args...)

	stdout, stderr, err := s.incus.ExecWithOutput(containerName, cmd)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "exec agent-box compose %s in %s: %v\nstderr: %s", verb, containerName, err, stderr)
	}

	var env composeEnvelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		return nil, status.Errorf(codes.Internal, "parse agent-box envelope (stdout=%q stderr=%q): %v", stdout, stderr, err)
	}
	if !env.OK {
		// agent-box reported a structured failure — bubble up as
		// FailedPrecondition since the call shape was valid but the
		// in-LXC operation refused (e.g., dir doesn't exist, no
		// compose runtime on PATH).
		msg := env.Error
		if msg == "" {
			msg = "agent-box compose " + verb + ": unspecified failure"
		}
		return nil, status.Error(codes.FailedPrecondition, msg)
	}
	return env.Result, nil
}

// ---- Discover ------------------------------------------------------

// Discover reads another box's filesystem, so it is tenant-scoped despite
// being read-only: the compose stacks on a tenant's box are that tenant's
// business (#1716).
func (s *ComposeAutostartServer) Discover(ctx context.Context, req *pb.DiscoverRequest) (*pb.DiscoverResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeContainersRead); err != nil {
		return nil, err
