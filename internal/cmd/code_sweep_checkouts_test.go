package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeCheckoutSweeper struct {
	got  *pb.SweepBoxRunCheckoutsRequest
	resp *pb.SweepBoxRunCheckoutsResponse
	err  error
}

func (f *fakeCheckoutSweeper) SweepBoxRunCheckouts(req *pb.SweepBoxRunCheckoutsRequest) (*pb.SweepBoxRunCheckoutsResponse, error) {
	f.got = req
	return f.resp, f.err
}

func sweepResp(d pb.BoxRunCheckoutDisposition) *pb.SweepBoxRunCheckoutsResponse {
	at := timestamppb.New(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	return &pb.SweepBoxRunCheckoutsResponse{
		PlatformRuns: true,
		MaxAge:       durationpb.New(7 * 24 * time.Hour),
		Checkouts: []*pb.BoxRunCheckout{
			{User: "alice", Name: "repo-run-0123abcd", SizeBytes: 2048, NewestModification: at, Disposition: d},
			{User: "alice", Name: "repo-run-ffffffff", Disposition: pb.BoxRunCheckoutDisposition_BOX_RUN_CHECKOUT_DISPOSITION_KEPT_SYMLINK},
