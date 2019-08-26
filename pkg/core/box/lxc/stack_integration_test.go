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

// #2163: the stack a box was created with is stored in Incus config and read
// back by BOTH real reads (GetContainer and ListContainers). Unit tests fake
// the Incus side, so deleting either read leaves them green; only a real
// instance settles it.
func TestIntegrationIncus_StackRoundTripsThroughGetAndList(t *testing.T) {
	client := incusenv.Require(t)
	if err := client.InitializeInfrastructure(incus.DefaultNetworkConfig()); err != nil {
		t.Fatalf("the daemon's own infrastructure init failed: %v", err)
	}

	const stack = "nodejs"
	base := fmt.Sprintf("ci%dstk", os.Getpid())
	withTenant, withoutTenant := base+"a", base+"b"
	withInstance, withoutInstance := withTenant+"-container", withoutTenant+"-container"
	t.Cleanup(func() { incusenv.DeleteInstance(t, withInstance) })
	t.Cleanup(func() { incusenv.DeleteInstance(t, withoutInstance) })

	backend := New(container.NewWithBackend(client))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
