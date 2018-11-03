package server

import (
	"context"

	"github.com/footprintai/containarium/internal/app"
)

// routeLister is the subset of *app.RouteStore that ContainerServer
// touches. Introduced so waitForContainerReady's dial loop can be
