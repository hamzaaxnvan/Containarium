package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestParsePassthroughRouteProtocol(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    pb.RouteProtocol
		wantErr bool
	}{
		{"empty defaults to tcp", "", pb.RouteProtocol_ROUTE_PROTOCOL_TCP, false},
		{"tcp", "tcp", pb.RouteProtocol_ROUTE_PROTOCOL_TCP, false},
		{"udp", "udp", pb.RouteProtocol_ROUTE_PROTOCOL_UDP, false},
		{"uppercase TCP", "TCP", pb.RouteProtocol_ROUTE_PROTOCOL_TCP, false},
		{"uppercase UDP", "UDP", pb.RouteProtocol_ROUTE_PROTOCOL_UDP, false},
		{"padded", "  udp  ", pb.RouteProtocol_ROUTE_PROTOCOL_UDP, false},
