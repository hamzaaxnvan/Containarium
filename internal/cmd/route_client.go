package cmd

import (
	"context"
	"fmt"

	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/pkg/core/expose"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// routeAPI is the subset of client methods the route / expose-port /
// passthrough-route verbs need. Both *client.GRPCClient and
// *client.HTTPClient satisfy it, so those verbs can honor --http the same
// way list / create / connect / … already do — instead of hardcoding gRPC
// and failing with an opaque name-resolver error against an https://
// server. See #909.
type routeAPI interface {
	AddRoute(domain, targetIP string, targetPort int32, containerName, description string) (*pb.ProxyRoute, error)
	ListRoutes(username string, activeOnly bool) ([]*pb.ProxyRoute, int32, error)
	DeleteRoute(domain string) error
	AddPassthroughRoute(externalPort, targetPort int32, targetIP string, protocol pb.RouteProtocol, containerName, description string) (*pb.PassthroughRoute, error)
	ListPassthroughRoutes() ([]*pb.PassthroughRoute, int32, error)
	DeletePassthroughRoute(externalPort int32, protocol pb.RouteProtocol) error
	ListContainers() ([]incus.ContainerInfo, error)
	Close() error
}

// newRouteClient returns an HTTP client in --http mode (when a --server is set)
