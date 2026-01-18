package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/gatewayprovider"
	"github.com/footprintai/containarium/internal/modelgateway"
	"github.com/footprintai/containarium/internal/secrets"
	"github.com/footprintai/containarium/pkg/core/box"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ModelGatewayService (#1726) — the admin + mint surface around the model
// gateway's HTTP data plane.
//
// Everything here is authz first. Two scopes that must not substitute for one
// another (gateway:admin registers a real upstream key; gateway:mint issues a
// scoped token), and one ownership rule: a caller may only mint for a box it
// owns. The rest of the file is plumbing around those two facts.

// Gateway-token lifetime. Per the design note, a `code run` token is a per-run
// lease: 24h by default, capped server-side at the same figure, so a leaked
// token is one box, one provider, optionally one model, for at most a day — and
// revocable by jti before then.
//
// The cap is the SERVER's. A caller may ask for less and get it; a caller asking
// for more is capped rather than rejected, and the response's expires_at (which
// equals the signed exp) is the only truth about when it dies.
const (
	DefaultGatewayTokenTTL = 24 * time.Hour
	MaxGatewayTokenTTL     = 24 * time.Hour
)

// ErrBoxNotFound is returned by a BoxAttributionLookup when no box exists for a
// tenant. It is deliberately the same outcome the server reports for a box that
// belongs to somebody else — see resolveBoxKeyOwner.
var ErrBoxNotFound = errors.New("server: no such box")

// GatewayKeyStore is the per-owner provider-key custody this service needs.
// Satisfied by *secrets.Store; an interface so the service can be tested without
// Postgres and so nothing here can reach the tenant-facing secret verbs by
// accident.
type GatewayKeyStore interface {
	SetGatewayProviderKey(ctx context.Context, keyOwner, provider, key string) error
	DeleteGatewayProviderKey(ctx context.Context, keyOwner, provider string) error
	GatewayProviderKeyStatus(ctx context.Context, keyOwner, provider string) (secrets.GatewayKeyStatus, error)
	KeyFor(ctx context.Context, keyOwner, provider string) (string, bool)
}

// BoxAttributionLookup answers the one question the mint path asks of the box
// store: does this tenant have a box, and is that box attributed to a cloud
// organization?
//
// Narrow on purpose. The mint path must not be able to read, mutate or enumerate
// boxes — it needs existence plus one label.
type BoxAttributionLookup interface {
	// BoxAttribution returns the box's cloud-org id, or "" when the box exists
	// but carries no attribution. Returns ErrBoxNotFound when there is no box.
	BoxAttribution(ctx context.Context, tenant string) (orgID string, err error)
}

// ModelGatewayServer implements pb.ModelGatewayServiceServer.
type ModelGatewayServer struct {
	pb.UnimplementedModelGatewayServiceServer

	keys  GatewayKeyStore
	boxes BoxAttributionLookup

	// gw is the running model gateway. Nil when this daemon does not serve one
	// (no provider key and no operator-registered upstream), in which case the
	// mint and list verbs refuse rather than hand back a token that could never
	// be used.
	gw *modelgateway.Gateway

	// secret signs gateway tokens — the daemon's jwt secret, the same trust root
	// the gateway verifies with. No new PKI.
	secret []byte
	// gatewayHost is the host a BOX reaches this daemon's gateway on (the bridge
	// gateway IP). Empty means the daemon cannot name a reachable base URL, and
	// minting refuses rather than return one that is wrong.
	gatewayHost string
	httpPort    int
}

// NewModelGatewayServer wires the service. gw/secret/gatewayHost may be zero on
// a daemon that does not serve the model gateway; the key verbs still work
// (a control plane can pre-register keys), the token verbs refuse.
func NewModelGatewayServer(keys GatewayKeyStore, boxes BoxAttributionLookup, gw *modelgateway.Gateway, secret []byte, gatewayHost string, httpPort int) *ModelGatewayServer {
	return &ModelGatewayServer{keys: keys, boxes: boxes, gw: gw, secret: secret, gatewayHost: gatewayHost, httpPort: httpPort}
}
