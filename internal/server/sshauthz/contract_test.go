// Package sshauthz_test holds the SSHAuthzService contract test (#2516):
// the generated gateway and messages, independent of the daemon's server
// (internal/server.SSHAuthzServer).
package sshauthz_test

import (
	"bytes"
	"context"
	"io"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/gateway"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// echoServer checks the request it receives and answers with resp.
type echoServer struct {
	pb.UnimplementedSSHAuthzServiceServer
	t    *testing.T
	want *pb.AuthorizeSSHRequest
	resp *pb.AuthorizeSSHResponse
}

func (s *echoServer) Authorize(_ context.Context, req *pb.AuthorizeSSHRequest) (*pb.AuthorizeSSHResponse, error) {
	if !proto.Equal(req, s.want) {
		s.t.Errorf("server received %v, want %v", req, s.want)
	}
	return s.resp, nil
}

type contractCase struct {
	name string
	req  *pb.AuthorizeSSHRequest
	resp *pb.AuthorizeSSHResponse
}

// contractCases is one ALLOW plus one DENY per SSHDenyReason value, with
// every field populated somewhere: both credential arms (a max uint64
// serial), every key type, cache_ttl, and a device approval.
func contractCases() []contractCase {
	reasons := slices.Sorted(maps.Keys(pb.SSHDenyReason_name))
	keyTypes := slices.Sorted(maps.Keys(pb.SSHKeyType_name))

	// Every third case is a certificate; the rest walk the key types, so
	// with one ALLOW plus one DENY per reason every key type is covered.
	nextKeyType := 0
	credential := func(i int) *pb.SSHCredential {
		if i%3 == 2 {
			return &pb.SSHCredential{Kind: &pb.SSHCredential_Certificate{Certificate: &pb.SSHCertificateCredential{
				KeyId:               "alice@example.com",
				Serial:              math.MaxUint64,
				CaFingerprintSha256: "SHA256:ca",
			}}}
		}
		kt := pb.SSHKeyType(keyTypes[nextKeyType%len(keyTypes)])
		nextKeyType++
		return &pb.SSHCredential{Kind: &pb.SSHCredential_PublicKey{PublicKey: &pb.SSHPublicKeyCredential{
			FingerprintSha256: "SHA256:key",
			KeyType:           kt,
		}}}
	}
	request := func(i int) *pb.AuthorizeSSHRequest {
		return &pb.AuthorizeSSHRequest{
			Login:      "alice",
			Credential: credential(i),
			ClientIp:   "192.0.2.10",
			SentinelId: "sentinel-1",
			ApprovalId: "appr-1",
		}
	}

	cases := []contractCase{{
		name: "ALLOW",
		req:  request(0),
		resp: &pb.AuthorizeSSHResponse{
			Decision: pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_ALLOW,
			UserId:   "user-1",
			CacheTtl: durationpb.New(30 * time.Second),
		},
	}}
	for i, r := range reasons {
		reason := pb.SSHDenyReason(r)
		resp := &pb.AuthorizeSSHResponse{
