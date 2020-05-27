package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func validateTestRequest() *pb.CreateContainerRequest {
	return &pb.CreateContainerRequest{Username: "alice", Stack: "nodejs"}
}

// fakeCreateClient answers CreateContainer only; any other method on the
// embedded nil interface would panic, which is the point: a validation must
// not call anything else.
type fakeCreateClient struct {
	pb.ContainerServiceClient
	got  *pb.CreateContainerRequest
	resp *pb.CreateContainerResponse
	err  error
}

func (f *fakeCreateClient) CreateContainer(_ context.Context, in *pb.CreateContainerRequest, _ ...grpc.CallOption) (*pb.CreateContainerResponse, error) {
	f.got = in
	return f.resp, f.err
}

// #2925: the request carries dry_run, the caller's request is not mutated, and
// only an answer that echoes dry_run counts as "accepted, nothing created".
func TestGRPCValidateCreate_SendsDryRunAndRequiresTheEcho(t *testing.T) {
	f := &fakeCreateClient{resp: &pb.CreateContainerResponse{DryRun: true}}
	c := &GRPCClient{client: f}
	req := validateTestRequest()

	if err := c.ValidateCreate(req); err != nil {
