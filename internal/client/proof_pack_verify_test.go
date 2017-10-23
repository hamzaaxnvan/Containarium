package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #3064 — both transports expose the same call.
var (
	_ interface {
		VerifyProofPack(context.Context, *pb.SealedProofPack) (*pb.VerifyProofPackResponse, error)
	} = (*HTTPClient)(nil)
	_ interface {
		VerifyProofPack(context.Context, *pb.SealedProofPack) (*pb.VerifyProofPackResponse, error)
	} = (*GRPCClient)(nil)
)

func TestHTTPClient_VerifyProofPack(t *testing.T) {
	var gotPath, gotAuth string
	var gotReq pb.VerifyProofPackRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		if err := protojson.Unmarshal(b, &gotReq); err != nil {
			t.Errorf("body is not protojson of the request: %v", err)
