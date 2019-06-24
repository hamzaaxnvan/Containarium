// Package sshauthz contains the daemon-side SSH authorization decision seam.
package sshauthz

import (
	"context"
	"fmt"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc"
)

// LocalFacts checks the facts that must be true before a login can be sent to
// a policy authority: the jump account is hosted locally, it is not orphaned,
// collaborator mappings resolve, and the box is still present.
//
// The checker returns the locally-known user id for callers that need it in a
// denial response. A false result is a normal NO_ACCESS decision; an error is
// also fail-closed and must never reach the cloud.
type LocalFacts interface {
	Check(context.Context, *pb.AuthorizeSSHRequest) (userID string, ok bool, err error)
}

// LocalFactsFunc adapts a function to LocalFacts for server wiring and tests.
type LocalFactsFunc func(context.Context, *pb.AuthorizeSSHRequest) (string, bool, error)

func (f LocalFactsFunc) Check(ctx context.Context, req *pb.AuthorizeSSHRequest) (string, bool, error) {
	return f(ctx, req)
}

// CloudAuthorizer is intentionally narrower than the generated client. The
// production cloud.Client and a small fake both satisfy it, while the
// decision logic remains independent of the rest of the ActuationService.
type CloudAuthorizer interface {
	AuthorizeSSH(context.Context, *pb.AuthorizeSSHRequest, ...grpc.CallOption) (*pb.AuthorizeSSHResponse, error)
}

// Decider answers the daemon's SSHAuthzService request. Managed is selected
// from enrollment state by the server wiring; it is not a request flag.
