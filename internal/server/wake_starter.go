package server

import (
	"context"
	"fmt"
	"log"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// WakeStarter is the adapter that the wake.WakeProxy uses to start a
// sleeping container in response to an inbound HTTP request. It wraps
// ContainerServer.StartContainer with WaitForReady=true so the wake
// proxy can hold the request until the container's primary port is
// dial-ready.
//
// Lives in the server package (rather than wake) so the wake package
// doesn't have to depend on the gRPC pb types or the full
// ContainerServer surface — it just sees the narrow interface
