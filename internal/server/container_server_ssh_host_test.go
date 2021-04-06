package server

import (
	"testing"

	boxlxc "github.com/footprintai/containarium/pkg/core/box/lxc"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// newSSHHostTestServer wires a ContainerServer over a MockBackend seeded
// with one running container, with the daemon's --ssh-host value set to
// sshHost. Mirrors newTTLTestServer's shape.
func newSSHHostTestServer(t *testing.T, sshHost string) *ContainerServer {
	t.Helper()
	mock := incustest.NewMockBackend()
	mock.Containers["alice-container"] = &incus.ContainerInfo{
		Name:      "alice-container",
		State:     "Running",
		IPAddress: "10.0.0.5",
	}
