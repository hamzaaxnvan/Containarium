//go:build !windows && !containarium_client

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/internal/proofpack"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// `containarium proof-pack verify` (#2798). Like `audit verify`, it reads the
// audit chain straight from Postgres, so it is a server-side command.

// proofPackVerifyChain opens the audit chain verify checks against; tests
// replace it.
var proofPackVerifyChain = func(ctx context.Context) (proofpack.SealChain, func(), error) {
	store, cleanup, err := openAuditStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	return store, cleanup, nil
}

// proofPackVerifyAPI is the one call --server makes; both client.GRPCClient
// and client.HTTPClient satisfy it (#3064).
type proofPackVerifyAPI interface {
	VerifyProofPack(ctx context.Context, sealed *pb.SealedProofPack) (*pb.VerifyProofPackResponse, error)
}

// newProofPackVerifyAPI dials the daemon named by the global --server/--http
// flags; tests replace it.
var newProofPackVerifyAPI = func() (proofPackVerifyAPI, func(), error) {
	if httpMode {
		h, err := client.NewHTTPClient(serverAddr, authToken)
		if err != nil {
			return nil, nil, err
		}
		return h, func() { _ = h.Close() }, nil
	}
	g, err := client.NewGRPCClient(serverAddr, certsDir, insecure)
	if err != nil {
		return nil, nil, err
	}
	return g, func() { _ = g.Close() }, nil
}

var proofPackVerifyCmd = &cobra.Command{
	Use:   "verify <pack.json>",
	Short: "Verify a sealed Proof Pack against its audit chain entry",
	Long: `Verify a sealed Proof Pack, the file attached to an agent-opened PR.

It recomputes every step's log digest and the digest of every step and of
the pack header, and compares them with the seal row in the audit hash
chain that the pack's seal points at. It then walks the chain from that row
to the tip. The first part that does not match is named:

  proofpack: step 1 ("lint"): digest ... does not match sealed ...

The seal row stays verifiable after retention purges older rows of the
chain. Once retention purges the seal row itself, verify says so and exits
non-zero. The chain walk proves internal consistency only; run
'audit verify-anchor' to rule out a rewrite-and-recompute of the chain.

Direct Postgres access, configured as for 'audit verify'.

With --server on the command line, the pack is sent to that daemon's
VerifyProofPack API instead (needs the audit:read scope), so it works from
any machine; no Postgres is read. A daemon's "ok" is only as trustworthy as
