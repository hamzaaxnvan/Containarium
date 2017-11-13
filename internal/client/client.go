package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/footprintai/containarium/internal/mtls"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ErrNoServerConfigured is what NewGRPCClient/NewHTTPClient return when
// handed an empty server address, instead of attempting to dial it. This is
// the single seam a remote or hybrid command in the containarium client
// passes through when --server, CONTAINARIUM_SERVER, and credentials.json's
// default_server all resolved to nothing — see
// docs/architecture/cli-client-server-split.md ("Server resolution in the
