package server

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/footprintai/containarium/internal/proofpack"
	"github.com/footprintai/containarium/internal/runlease"
	"github.com/footprintai/containarium/internal/tracker/submit"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Proof Pack on the submit path (#2798, slice 2 of #2366). Before the change
// is pushed, the daemon generates the run's Proof Pack in the run's box,
// seals it into the audit hash chain, and the pushed branch carries it as
// proofpack.AttachPath; the description carries its digest. A change is
// never opened without its pack.
//
// The pack is produced in the run's own box for now. #2799 moves production
// to a box other than the coder's.

// proofPackExecerForSubmit is proofPackExecer if a test set one, otherwise
// s.manager, or nil when neither is configured (never a typed nil).
func (s *ContainerServer) proofPackExecerForSubmit() proofpack.Execer {
	if s.proofPackExecer != nil {
