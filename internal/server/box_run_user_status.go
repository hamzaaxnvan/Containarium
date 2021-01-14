package server

import (
	"context"
	"strings"

	"github.com/footprintai/containarium/internal/coderun/engine"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// boxRunUserExistsScript is run as root and prints one word. It never touches
// the run user's files.
func boxRunUserExistsScript() string {
	return `if id -u ` + boxRunUnprivilegedUser + ` >/dev/null 2>&1; then echo yes; else echo no; fi` + "\n"
}

// GetBoxRunUserStatus reports, read-only, what StartBoxRun would conclude about
// the run user's sign-in on a box (#3274): the same level lookup and the same
// credential-status probe, with nothing queued, rotated or provisioned. A box
// with no recorded level is reported as such (platform runs use the owner's
// identity there). On a recorded box whose run user does not exist yet the state
// is NONE, which is what the first run would refuse on once it created the user.
// Names the credential source only, never a value.
func (s *ContainerServer) GetBoxRunUserStatus(ctx context.Context, req *pb.GetBoxRunUserStatusRequest) (*pb.GetBoxRunUserStatusResponse, error) {
	if err := validateBoxRunOwner(req.GetUsername()); err != nil {
		return nil, err
	}
	if err := s.authorizeBoxRuns(ctx, req.GetUsername()); err != nil {
		return nil, err
	}
	listExec := s.boxRunExecFn()
	startExec := s.boxRunStartExecFn()
	if listExec == nil || startExec == nil {
		return nil, status.Error(codes.Unavailable, "no container backend to check the run user on")
	}
