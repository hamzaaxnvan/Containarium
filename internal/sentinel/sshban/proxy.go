package sshban

import (
	"context"
	"net/http"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// Proxy forwards admin calls to the process that actually enforces each policy.
// Callers must mount its REST handler behind the sentinel ADMIN HMAC middleware.
type Proxy struct {
	pb.UnimplementedSentinelSSHBanServiceServer
	Sockets map[pb.SSHBanListener]string
	// ResolveSocket permits live listener discovery after metadata reconciliation.
	ResolveSocket func(pb.SSHBanListener) string
