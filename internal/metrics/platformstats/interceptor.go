package platformstats

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// UnaryInterceptor returns a grpc.UnaryServerInterceptor that classifies
// every completed unary RPC by its final gRPC status and records one
// event into stats — the single choke point where both native gRPC
