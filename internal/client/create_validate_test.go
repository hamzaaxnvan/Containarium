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
		t.Fatalf("ValidateCreate on an accepting daemon: %v", err)
	}
	if !f.got.GetDryRun() {
		t.Error("the daemon was not sent dry_run=true: this would have been a real create")
	}
	if req.GetDryRun() {
		t.Error("the caller's request was mutated")
	}
}

func TestGRPCValidateCreate_RejectionIsAnError(t *testing.T) {
	c := &GRPCClient{client: &fakeCreateClient{err: errors.New(`unknown stack "typo"`)}}
	err := c.ValidateCreate(validateTestRequest())
	if err == nil || !strings.Contains(err.Error(), `unknown stack "typo"`) {
		t.Fatalf("got %v, want the daemon's rejection", err)
	}
}

// A daemon that predates dry_run ignores it and really creates the box; that
// success must not be read as "validated".
func TestGRPCValidateCreate_NoEchoMeansTheDaemonDidNotValidate(t *testing.T) {
	c := &GRPCClient{client: &fakeCreateClient{resp: &pb.CreateContainerResponse{Container: &pb.Container{Name: "alice-container"}}}}
	err := c.ValidateCreate(validateTestRequest())
