package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/anonbox"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/box"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

type fakeAnonEnsurer struct {
	got anonbox.EnsureRequest
	res *anonbox.EnsureResult
	err error
}

func (f *fakeAnonEnsurer) Ensure(_ context.Context, req anonbox.EnsureRequest) (*anonbox.EnsureResult, error) {
	f.got = req
	return f.res, f.err
}

type fakeAnonLister struct{ boxes []box.BoxStatus }

func (f *fakeAnonLister) List(context.Context) ([]box.BoxStatus, error) { return f.boxes, nil }

func anonSrv(ens *fakeAnonEnsurer, lst *fakeAnonLister) *AnonymousBoxServer {
	if ens == nil {
		ens = &fakeAnonEnsurer{res: &anonbox.EnsureResult{BoxName: "anon-deadbeef-container", SSHHost: "10.0.0.9", SSHPort: 22, SSHUser: "anon-deadbeef"}}
	}
	if lst == nil {
		lst = &fakeAnonLister{}
	}
