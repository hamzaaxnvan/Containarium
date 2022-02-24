package cmd

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func execMinerSig(t *testing.T, build func() *cobra.Command, args ...string) (string, error) {
	t.Helper()
	cmd := build()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestMinerSignaturesList_RendersBaselineAndOperator(t *testing.T) {
	reply, _ := protojson.Marshal(&pb.ListMinerSignaturesResponse{
		BaselineVersion: "1",
		Signatures: []*pb.MinerSignature{
			{Kind: pb.MinerSignatureKind_MINER_SIGNATURE_KIND_NAME, Pattern: "xmrig", Label: "XMRig", Builtin: true},
			{Kind: pb.MinerSignatureKind_MINER_SIGNATURE_KIND_ARGV, Pattern: "--coin ", Label: "coin flag", Builtin: true},
			{Kind: pb.MinerSignatureKind_MINER_SIGNATURE_KIND_SHA256, Pattern: strings.Repeat("ab", 32), Label: "in-house build"},
		},
	})
	ps := &policyServer{reply: string(reply)}
	ps.start(t)
	out, err := execMinerSig(t, newMinerSignaturesListCmd)
	if err != nil {
		t.Fatal(err)
	}
	if ps.method != http.MethodGet || ps.path != "/v1/security/miner-signatures" {
