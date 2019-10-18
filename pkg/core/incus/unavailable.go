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

// Compile-time assertion.
var _ Backend = (*UnavailableBackend)(nil)

// StoragePool returns the default name rather than an error: it has no
// signature to fail through, and a caller on a host with no incus never
// reaches a create path where the value is used.
func (*UnavailableBackend) StoragePool() string { return DefaultStoragePool }

func (*UnavailableBackend) CreateContainer(ContainerConfig) error       { return ErrUnavailable }
func (*UnavailableBackend) StartContainer(string) error                 { return ErrUnavailable }
func (*UnavailableBackend) StopContainer(string, bool) error            { return ErrUnavailable }
func (*UnavailableBackend) DeleteContainer(string) error                { return ErrUnavailable }
func (*UnavailableBackend) GetContainer(string) (*ContainerInfo, error) { return nil, ErrUnavailable }
func (*UnavailableBackend) ListContainers() ([]ContainerInfo, error)    { return nil, ErrUnavailable }
func (*UnavailableBackend) WaitForNetwork(string, time.Duration) (string, error) {
	return "", ErrUnavailable
}
func (*UnavailableBackend) Exec(string, []string) error                { return ErrUnavailable }
func (*UnavailableBackend) EnsureToolchainVolume(string, string) error { return ErrUnavailable }
func (*UnavailableBackend) ExecWithOutput(string, []string) (string, string, error) {
	return "", "", ErrUnavailable
}
func (*UnavailableBackend) ExecWithExitCode(string, []string) (string, string, int, error) {
	return "", "", 0, ErrUnavailable
}
func (*UnavailableBackend) WriteFile(string, string, []byte, string) error { return ErrUnavailable }
func (*UnavailableBackend) ReadFile(string, string) ([]byte, error)        { return nil, ErrUnavailable }
