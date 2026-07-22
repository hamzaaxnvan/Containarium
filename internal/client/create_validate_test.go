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
