package server

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/anonbox"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

type fakeAnonClaimer struct {
	got anonbox.ClaimRequest
	res *anonbox.ClaimResult
	err error
}

func (f *fakeAnonClaimer) Claim(_ context.Context, req anonbox.ClaimRequest) (*anonbox.ClaimResult, error) {
	f.got = req
	return f.res, f.err
}

func TestClaimAnonymousBox_MapsRequestAndResponse(t *testing.T) {
	cl := &fakeAnonClaimer{res: &anonbox.ClaimResult{BoxName: "anon-1a2b3c4d-container", Tenant: "qa-claim", SSHUser: "anon-1a2b3c4d"}}
	srv := anonSrv(nil, nil)
	srv.SetClaimer(cl)

	out, err := srv.ClaimAnonymousBox(testCtx(), &pb.ClaimAnonymousBoxRequest{
		ClaimToken: " v1.anon-1a2b3c4d-container.ff.1.id.mac ", Tenant: "qa-claim", AuthorizedKeys: []string{"ssh-ed25519 AAAA k"},
	})
	if err != nil {
