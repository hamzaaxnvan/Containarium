package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/anonbox"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/box"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// BoxBackend exposes the runtime-neutral box backend so sibling services
// (the anonymous-box door) can be built over the same backend the
// container service uses.
func (s *ContainerServer) BoxBackend() box.BoxBackend { return s.boxBackend }

// AnonEnsurer is the slice of anonbox.Manager the server calls. An
// interface so the authz and mapping tests run against a fake.
type AnonEnsurer interface {
	Ensure(ctx context.Context, req anonbox.EnsureRequest) (*anonbox.EnsureResult, error)
}

// AnonClaimer is the slice of anonbox.Manager that redeems a claim token.
type AnonClaimer interface {
	Claim(ctx context.Context, req anonbox.ClaimRequest) (*anonbox.ClaimResult, error)
}

// AnonDoor is the operator state slice of anonbox.Manager (#2200).
type AnonDoor interface {
	DoorConfig() anonbox.DoorConfig
	SetDoorConfig(anonbox.DoorConfig) error
	DoorErr() error
}

// AnonLister lists boxes so ListAnonymousBoxes can filter on the
// anon.fingerprint label. Satisfied by any box.BoxBackend.
type AnonLister interface {
	List(ctx context.Context) ([]box.BoxStatus, error)
}

// AnonymousBoxServer implements AnonymousBoxService (#2197, PR B) over
// internal/anonbox. Claim (#2199) and SetAnonymousDoorConfig (#2200) are
// registered so the contract is complete and authz-gated, but return
// Unimplemented until their issues land.
type AnonymousBoxServer struct {
	pb.UnimplementedAnonymousBoxServiceServer
	ensurer AnonEnsurer
	lister  AnonLister
	claimer AnonClaimer // nil = claims not enabled on this daemon
	door    AnonDoor    // nil = no kill switch / bans (always open)
