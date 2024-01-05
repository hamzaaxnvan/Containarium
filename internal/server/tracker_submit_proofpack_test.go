package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/footprintai/containarium/internal/proofpack"
	"github.com/footprintai/containarium/internal/tracker"
	"github.com/footprintai/containarium/internal/tracker/submit"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeProofBox is a proofpack.Execer standing in for the run's box: the
// probe reports probeRC (12: a Makefile, no declaration, so `make test`
// runs), the one step prints "PASS", and HEAD is commit.
type fakeProofBox struct {
	commit  string
	probeRC int
	ran     []string
}

func (f *fakeProofBox) ExecWithExitCode(box string, argv []string) (string, string, int, error) {
	f.ran = append(f.ran, box)
	switch {
	case len(argv) > 3 && argv[3] == "proofpack-probe":
		return "", "", f.probeRC, nil
	case len(argv) > 0 && argv[0] == "git":
		return f.commit + "\n", "", 0, nil
	default:
