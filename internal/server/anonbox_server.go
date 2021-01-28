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
	limits  anonbox.Limits
}

// SetDoor enables Get/SetAnonymousDoorConfig over the manager's door
// state (#2200).
func (s *AnonymousBoxServer) SetDoor(d AnonDoor) { s.door = d }

// SetClaimer enables ClaimAnonymousBox (#2199). Without it the RPC is
// gated but Unimplemented — a daemon without a claim secret cannot
// verify a token.
func (s *AnonymousBoxServer) SetClaimer(c AnonClaimer) { s.claimer = c }

// NewAnonymousBoxServer wires the service over a manager and a lister.
func NewAnonymousBoxServer(ensurer AnonEnsurer, lister AnonLister, limits anonbox.Limits) *AnonymousBoxServer {
	return &AnonymousBoxServer{ensurer: ensurer, lister: lister, limits: limits}
}

// EnsureAnonymousBox — admin or anon:door. The sentinel's door plugin is
// the intended caller: on the REST shim its request signature is accepted
// in place of a JWT and mapped to the anon:door scope (see
// gateway.anonDoorHandler), so this guard sees the same identity either way.
func (s *AnonymousBoxServer) EnsureAnonymousBox(ctx context.Context, req *pb.EnsureAnonymousBoxRequest) (*pb.EnsureAnonymousBoxResponse, error) {
	if err := auth.RequireRoleOrScope(ctx, auth.RoleAdmin, auth.ScopeAnonDoor); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetPublicKey()) == "" {
		return nil, status.Error(codes.InvalidArgument, "public_key is required")
	}
	res, err := s.ensurer.Ensure(ctx, anonbox.EnsureRequest{
		Fingerprint: req.GetFingerprint(),
		PublicKey:   req.GetPublicKey(),
		SourceIP:    req.GetSourceIp(),
	})
	if err != nil {
		return nil, anonErrToStatus(err)
	}
	out := &pb.EnsureAnonymousBoxResponse{
		BoxName:         res.BoxName,
		SshHost:         res.SSHHost,
		SshPort:         int32(res.SSHPort), //nolint:gosec // a port
		SshUser:         res.SSHUser,
		Reused:          res.Reused,
		PreviousExpired: res.PreviousExpired,
	}
	if !res.TTLExpiresAt.IsZero() {
		out.TtlExpiresAt = timestamppb.New(res.TTLExpiresAt)
	}
	return out, nil
}

// ClaimAnonymousBox — admin or anon:admin (the cloud control plane after
// signup, or an operator via `containarium anon claim`). Redeems the
// single-use token and binds the box to the tenant (#2199).
func (s *AnonymousBoxServer) ClaimAnonymousBox(ctx context.Context, req *pb.ClaimAnonymousBoxRequest) (*pb.ClaimAnonymousBoxResponse, error) {
	if err := auth.RequireRoleOrScope(ctx, auth.RoleAdmin, auth.ScopeAnonAdmin); err != nil {
		return nil, err
	}
