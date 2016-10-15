package sshsession

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/proto"
)

type authorizationCaptureClient struct{ records []*pb.SSHSessionRecord }

func (c *authorizationCaptureClient) IngestSSHSessionRecords(_ context.Context, req *pb.IngestSSHSessionRecordsRequest) (*pb.IngestSSHSessionRecordsResponse, error) {
	resp := &pb.IngestSSHSessionRecordsResponse{}
	for _, record := range req.Records {
		c.records = append(c.records, proto.Clone(record).(*pb.SSHSessionRecord))
		resp.Outcomes = append(resp.Outcomes, pb.IngestOutcome_INGEST_OUTCOME_INSERTED)
		resp.Inserted++
	}
	return resp, nil
}

type authorizationTestTokens struct{}

func (authorizationTestTokens) TokenFor(string) (string, bool, error) { return "test-token", true, nil }
func (authorizationTestTokens) Backends() ([]string, error)           { return []string{"test-backend"}, nil }

func TestShipOnce_AuthorizationAndLegacyJSONL(t *testing.T) {
