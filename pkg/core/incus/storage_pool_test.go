package incus

import (
	"strings"
	"testing"
)

// #1213: every root-disk construction hardcoded the pool literally named
// "default", so a backend migrated onto an isolated storage pool silently
// un-migrated itself — existing tenants stayed put while every NEW tenant
// landed back on the shared-filesystem pool. The contention probe went clean
// right after the migration and regressed weeks later as tenants arrived,
// with nothing in the product explaining why.

func TestStoragePool_DefaultsWhenUnset(t *testing.T) {
	t.Cleanup(func() { configuredStoragePool.Store("") })
	configuredStoragePool.Store("")

	c := &Client{}
	if got := c.StoragePool(); got != DefaultStoragePool {
		t.Errorf("StoragePool() = %q, want %q", got, DefaultStoragePool)
	}
}

// The process-wide setting is what reaches the clients built by subsystems
// that never see daemon config — the cloud actuator and several
// container_server helpers each call incus.New() for themselves. A
// per-client-only setting would be honoured on the daemon's client and
// ignored by all of those, which is the same silent divergence being fixed.
func TestStoragePool_ProcessWideSettingReachesFreshClients(t *testing.T) {
	t.Cleanup(func() { configuredStoragePool.Store("") })

	SetDefaultStoragePool("tenants")

	// A client built with no knowledge of the daemon's configuration.
	if got := (&Client{}).StoragePool(); got != "tenants" {
		t.Errorf("a freshly built client reports pool %q, want %q — it would create containers "+
			"on the pool the operator migrated away from (#1213)", got, "tenants")
	}
}

// A per-client override still wins, so a test (or a future per-placement
// caller) can target one pool without disturbing the process default.
func TestStoragePool_PerClientOverrideWins(t *testing.T) {
	t.Cleanup(func() { configuredStoragePool.Store("") })
	SetDefaultStoragePool("tenants")

	c := &Client{}
	c.SetStoragePool("scratch")
	if got := c.StoragePool(); got != "scratch" {
		t.Errorf("StoragePool() = %q, want the per-client override %q", got, "scratch")
	}
}

// Empty must not clobber a configured pool — otherwise a subsystem that
// calls the setter with an unset flag would quietly reset every later
// client back to "default".
func TestSetDefaultStoragePool_EmptyIsIgnored(t *testing.T) {
	t.Cleanup(func() { configuredStoragePool.Store("") })

	SetDefaultStoragePool("tenants")
	SetDefaultStoragePool("")

	if got := (&Client{}).StoragePool(); got != "tenants" {
		t.Errorf("an empty SetDefaultStoragePool reset the configured pool to %q", got)
	}
}
