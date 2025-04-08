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

// The environment #1199 has been blocked on (#1332).
//
// Every other piece of per-tenant ZFS encryption is proven: the zfscrypt
// semantics against a real pool (#1200), the hooks' orchestration in unit
// tests, and PreCreate itself against a real pool. The one step left —
// wiring PreCreate into the create path — had nowhere to run, because
// nothing in CI could create a container through the daemon's own code
// against a real Incus on a real ZFS pool.
//
// This test is that environment. It deliberately drives the box seam
// (box.BoxBackend.Create → container.Manager.Create → the daemon's Incus
// client) rather than `incus launch`: the point is not that a container can
// exist on the runner, it is that OUR create path runs there. Driving Incus
// directly would prove the runner works and leave #1199 exactly as blocked
// as it was.

// tenant is a per-process name so a rerun on a dirty machine cannot collide
// with a leftover from a previous one. The daemon derives the instance name
// from it as "<tenant>-container".
func tenant() string { return fmt.Sprintf("ci%d", os.Getpid()) }

// TestIntegrationIncus_CreateThroughTheDaemonsOwnPath is #1332's acceptance
// criteria 1 and 2, in order: the daemon initialises Incus against a ZFS
// storage pool, then creates a container through its own Create() and the
// container is running.
func TestIntegrationIncus_CreateThroughTheDaemonsOwnPath(t *testing.T) {
	client := incusenv.Require(t)

	// AC1 — initialise through InitializeInfrastructure, which is what the
	// daemon itself calls at startup, not a hand-written preseed. If the
	// daemon's own storage selection cannot land on ZFS here, that is a
	// finding about the daemon and we want to see it as a failure.
	if err := client.InitializeInfrastructure(incus.DefaultNetworkConfig()); err != nil {
		t.Fatalf("the daemon's own infrastructure init failed: %v", err)
	}

	driver, err := client.PoolDriver(client.StoragePool())
	if err != nil {
		t.Fatalf("could not read the storage pool %q the daemon just ensured: %v", client.StoragePool(), err)
	}
	if driver != incus.StorageDriverZFS {
		// Not a cosmetic assertion. On the `dir` driver there is no
		// per-container dataset, so there is nothing for #1199 to encrypt
		// and the lane would prove nothing about the encrypted path while
		// still going green.
