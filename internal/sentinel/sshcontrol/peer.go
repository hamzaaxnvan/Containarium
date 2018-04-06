package sshcontrol

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

// Peer is a local plugin's bounded transport registry and generated RPC client.
type Peer struct {
	Client   pb.SentinelSSHUpstreamServiceClient
	listener pb.SSHBanListener
	conn     *grpc.ClientConn
	mu       sync.Mutex
	refs     map[string]peerRef
}

// peerRef expires abandoned setup metadata without retaining established sessions.
type peerRef struct {
	ref     *pb.SSHUpstreamConnection
	expires time.Time
}

// DialPeer connects only to the configured local operator socket.
func DialPeer(socket string, listener pb.SSHBanListener) (*Peer, error) {
	if !filepath.IsAbs(socket) || (listener != pb.SSHBanListener_SSH_BAN_LISTENER_MAIN && listener != pb.SSHBanListener_SSH_BAN_LISTENER_ANONYMOUS) {
		return nil, errors.New("private upstream socket and listener required")
	}
	conn, err := grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &Peer{Client: pb.NewSentinelSSHUpstreamServiceClient(conn), listener: listener, conn: conn, refs: map[string]peerRef{}}, nil
}

// Close releases the generated client's transport during plugin shutdown.
func (p *Peer) Close() error { return p.conn.Close() }

// CallContext bounds every private control operation independently of SSH traffic.
func CallContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, time.Second)
}

// Open records a controller-confirmed identity; substitution is never accepted.
func (p *Peer) Open(ctx context.Context, id, remote string) (*pb.SSHUpstreamConnection, error) {
	call, cancel := CallContext(ctx)
	defer cancel()
	ref := &pb.SSHUpstreamConnection{Listener: p.listener, ConnectionId: id, RemoteAddr: remote}
	r, err := p.Client.OpenSSHUpstreamConnection(call, &pb.OpenSSHUpstreamConnectionRequest{Connection: ref})
	if err != nil {
		return nil, err
	}
	ref.ControllerGeneration = r.ControllerGeneration
	p.mu.Lock()
