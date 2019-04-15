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

// #2859: installing a stack on a running box records it, visible through BOTH
// real reads (GetContainer and ListContainers); a second stack replaces the
// first; a base script (the startup timezone reconcile runs one in every box)
// does not touch it. Unit tests fake the Incus side, so only a real instance
// settles that the write and the reads agree.
func TestIntegrationIncus_InstallStackUpdatesStoredStack(t *testing.T) {
	client := incusenv.Require(t)
	if err := client.InitializeInfrastructure(incus.DefaultNetworkConfig()); err != nil {
		t.Fatalf("the daemon's own infrastructure init failed: %v", err)
	}

	tenant := fmt.Sprintf("ci%dist", os.Getpid())
	instance := tenant + "-container"
	t.Cleanup(func() { incusenv.DeleteInstance(t, instance) })

	mgr := container.NewWithBackend(client)
	backend := New(mgr)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()

	// Created with no stack: it starts out reporting none.
