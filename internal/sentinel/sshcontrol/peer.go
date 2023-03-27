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
