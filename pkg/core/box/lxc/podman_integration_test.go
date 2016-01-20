//go:build incus

package lxc

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/testsupport/incusenv"
	"github.com/footprintai/containarium/pkg/core/box"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #2858: the podman flag a box was created with is stored in Incus config and
// read back by BOTH real reads (GetContainer and ListContainers), in both
// directions; a box that predates the key reports true. Unit tests fake the
// Incus side, so deleting either read leaves them green.
func TestIntegrationIncus_PodmanEnabledRoundTripsThroughGetAndList(t *testing.T) {
	client := incusenv.Require(t)
	if err := client.InitializeInfrastructure(incus.DefaultNetworkConfig()); err != nil {
		t.Fatalf("the daemon's own infrastructure init failed: %v", err)
	}

	base := fmt.Sprintf("ci%dpod", os.Getpid())
	onTenant, offTenant, legacyName := base+"a", base+"b", base+"c-container"
	onInstance, offInstance := onTenant+"-container", offTenant+"-container"
	for _, inst := range []string{onInstance, offInstance, legacyName} {
		inst := inst
		t.Cleanup(func() { incusenv.DeleteInstance(t, inst) })
	}

	backend := New(container.NewWithBackend(client))
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()

