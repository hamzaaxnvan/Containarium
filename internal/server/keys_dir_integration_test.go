//go:build incus

package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/footprintai/containarium/internal/events"
	"github.com/footprintai/containarium/internal/metrics/platformstats"
	"github.com/footprintai/containarium/internal/testsupport/incusenv"
	boxlxc "github.com/footprintai/containarium/pkg/core/box/lxc"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/zfscrypt"
)

// #1342: --zfs-keys-dir is the switch. This drives the daemon's own startup
// wiring — keyProviderFromDir, SetKeyProvider, SetEncryptionStorage — rather
// than injecting a provider directly, so what is proven is the path an
// operator actually takes.
//
// Deliberately wires the hooks BEFORE custody, the opposite of dual_server's
// order. If SetKeyProvider ever stopped re-attaching to already-built hooks,
// the unit test would catch it and this would catch it against a real Incus:
// every encrypted create failing on a correctly-configured daemon.
func TestIntegrationIncus_KeysDirMakesEncryptedCreateWork(t *testing.T) {
	client := incusenv.Require(t)
	if err := client.InitializeInfrastructure(incus.DefaultNetworkConfig()); err != nil {
		t.Fatalf("the daemon's own infrastructure init failed: %v", err)
	}
	tenantRoot := DefaultTenantRoot(client)
	if tenantRoot == "" {
