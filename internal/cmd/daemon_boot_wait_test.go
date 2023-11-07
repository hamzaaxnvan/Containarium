//go:build !windows && !containarium_client

package cmd

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// Issue #1483: on a byoc node, `containarium.service` reports `active`
// forever while permanently stuck at "Waiting for core containers to be
// ready..." after boot. waitForCoreContainers already re-resolves each
// core container's address on every 2-second poll (it does not latch a
// stale IP) and already wraps its loop in a 2-minute context.WithTimeout.
// But that outer bound is only checked at the top of each loop iteration
// — it does nothing to bound a single incusClient.FindContainerByRole
// call *inside* one iteration. When the incus.Client passed in has no
// per-call HTTP timeout (the shared, long-lived client every other daemon
// code path uses), a slow-to-answer incusd can park the goroutine inside
// one such call for up to the SDK transport's own 3600s
// ResponseHeaderTimeout — 30x past the outer bound — and the loop never
// gets to retry.
//
// These tests exercise waitForCoreContainers directly against a fake
// Incus API server that answers the connection-probe endpoint
// (GetServer, "/1.0") quickly but never answers the instance-lookup
// endpoints FindContainerByRole's ListContainers hits ("/1.0/instances",
// "/1.0/instances/<name>") — standing in for an incusd that is alive and
// reachable (the daemon already connected to it successfully at startup)
