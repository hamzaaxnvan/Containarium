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
}

// Handler creates generated REST routes without introducing a public gRPC port.
func (p *Proxy) Handler() http.Handler {
	mux := runtime.NewServeMux()
	if err := pb.RegisterSentinelSSHBanServiceHandlerServer(context.Background(), mux, p); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "SSH ban API unavailable", http.StatusServiceUnavailable)
		})
	}
	return mux
}

// client validates the listener before touching any local control socket.
func (p *Proxy) client(listener pb.SSHBanListener) (pb.SentinelSSHBanServiceClient, func(), error) {
	if listener != pb.SSHBanListener_SSH_BAN_LISTENER_MAIN && listener != pb.SSHBanListener_SSH_BAN_LISTENER_ANONYMOUS {
		return nil, nil, status.Error(codes.InvalidArgument, "listener is required")
	}
	path := p.Sockets[listener]
	if p.ResolveSocket != nil {
		path = p.ResolveSocket(listener)
	}
	if path == "" {
