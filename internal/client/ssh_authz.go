package client

import (
	"context"
	"fmt"
	"io"
	"net/http"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// sshAuthzAuthorizePath is the REST route grpc-gateway maps
// SSHAuthzService.Authorize to (ssh_authz.proto).
const sshAuthzAuthorizePath = "/v1/ssh/authorize"

// AuthorizeSSH asks the daemon's SSHAuthzService.Authorize (#2516) whether
// req's credential may open a session as req.Login. The client's token must
// carry the ssh:authorize scope and the daemon must run with
// --ssh-authz-keys-command; otherwise the route is sentinel-only (#2527).
//
// A DENY is a normal response, not an error. An error means no decision was
