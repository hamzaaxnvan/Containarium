package incus

import (
	"errors"
	"io"
	"time"

	"github.com/lxc/incus/v7/shared/api"
)

// ErrUnavailable is returned by every UnavailableBackend method. It marks a
// host where incus is not reachable — e.g. the Kubernetes build variant
// running on a node with no incus daemon. Box lifecycle on such a host is
// served by a different box backend (pkg/core/box); any residual incus-only
// operation fails cleanly with this error instead of panicking on a nil
// client.
var ErrUnavailable = errors.New("incus backend not available on this host")

// UnavailableBackend implements Backend with every method returning
// ErrUnavailable. It lets the daemon construct a container.Manager on a host
// without incus so the process can still start; the box-lifecycle surface is
// served by another backend, and the legacy incus-only Manager methods return
// a clear error rather than crash.
type UnavailableBackend struct{}

// NewUnavailableBackend returns an UnavailableBackend.
func NewUnavailableBackend() *UnavailableBackend { return &UnavailableBackend{} }
