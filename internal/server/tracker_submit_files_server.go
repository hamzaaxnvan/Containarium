package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/tracker"
	"github.com/footprintai/containarium/internal/tracker/submit"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// filePusherForSubmit returns submitFilePusher if a test set one,
// otherwise a real submit.NewGitPusher().
func (s *ContainerServer) filePusherForSubmit() submit.FilePusher {
	if s.submitFilePusher != nil {
		return s.submitFilePusher
	}
	return submit.NewGitPusher()
}

// SubmitTrackerChangeFiles opens a change request from a file set over
// the connection's broker credential, with no run box (#3005): the
// files are committed in a fresh temporary repository on the host — on
// the head branch's tip when it already exists on the remote, otherwise
// on the base branch — and pushed without force, then the change is
// opened exactly as SubmitTrackerChange opens one, unless the head
// branch already has an open change, which is reused (#3288). Authorised like
// CommentOnTrackerIssue — tracker:write plus tenant authority on the
// connection — since there is no box for a run-scoped token to name.
func (s *ContainerServer) SubmitTrackerChangeFiles(ctx context.Context, req *pb.SubmitTrackerChangeFilesRequest) (*pb.SubmitTrackerChangeFilesResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeTrackerWrite); err != nil {
		return nil, err
	}
