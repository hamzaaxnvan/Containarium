package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestBackendStorageFromPool_MapsDriverAndIsolation pins the projection from
// the incus driver onto the wire contract.
//
// The isolation column is the point of #1209: an operator asking "which of my
// backends can have one tenant stall another's fsync?" (#1206) has to be able
// to answer it from the API, not from a startup log on each host.
func TestBackendStorageFromPool_MapsDriverAndIsolation(t *testing.T) {
	tests := []struct {
		name          string
		driver        incus.StorageDriver
		wantDriver    pb.StorageDriver
		wantIsolation pb.StorageIsolation
	}{
		{
			name:          "zfs isolates",
			driver:        incus.StorageDriverZFS,
			wantDriver:    pb.StorageDriver_STORAGE_DRIVER_ZFS,
			wantIsolation: pb.StorageIsolation_STORAGE_ISOLATION_PER_CONTAINER,
		},
		{
			name:          "btrfs isolates",
			driver:        incus.StorageDriverBtrfs,
			wantDriver:    pb.StorageDriver_STORAGE_DRIVER_BTRFS,
			wantIsolation: pb.StorageIsolation_STORAGE_ISOLATION_PER_CONTAINER,
		},
		{
			name:          "lvm isolates",
			driver:        incus.StorageDriverLVM,
			wantDriver:    pb.StorageDriver_STORAGE_DRIVER_LVM,
			wantIsolation: pb.StorageIsolation_STORAGE_ISOLATION_PER_CONTAINER,
		},
		{
			name:          "ceph isolates",
			driver:        incus.StorageDriverCeph,
			wantDriver:    pb.StorageDriver_STORAGE_DRIVER_CEPH,
			wantIsolation: pb.StorageIsolation_STORAGE_ISOLATION_PER_CONTAINER,
		},
		{
			name:          "dir shares one filesystem across tenants",
			driver:        incus.StorageDriverDir,
			wantDriver:    pb.StorageDriver_STORAGE_DRIVER_DIR,
			wantIsolation: pb.StorageIsolation_STORAGE_ISOLATION_SHARED_FILESYSTEM,
		},
		{
			name:   "a driver we do not classify is OTHER, not UNSPECIFIED",
			driver: incus.StorageDriver("some-future-driver"),
			// OTHER means "read, but not classified"; UNSPECIFIED is reserved
			// for "could not read the pool at all". Collapsing them would make
			// an observed-but-unknown driver indistinguishable from a failure.
			wantDriver:    pb.StorageDriver_STORAGE_DRIVER_OTHER,
			wantIsolation: pb.StorageIsolation_STORAGE_ISOLATION_UNKNOWN_DRIVER,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := backendStorageFromPool("default", tt.driver)
			if got == nil {
				t.Fatal("got nil for an observed driver")
			}
			if got.Driver != tt.wantDriver {
				t.Errorf("Driver = %v, want %v", got.Driver, tt.wantDriver)
			}
			if got.Isolation != tt.wantIsolation {
				t.Errorf("Isolation = %v, want %v", got.Isolation, tt.wantIsolation)
			}
			if got.Pool != "default" {
				t.Errorf("Pool = %q, want %q", got.Pool, "default")
			}
			// The raw name always travels, so a STORAGE_DRIVER_OTHER value is
			// still actionable for an operator.
			if got.DriverName != string(tt.driver) {
				t.Errorf("DriverName = %q, want %q", got.DriverName, tt.driver)
			}
		})
	}
}

// TestBackendStorageFromPool_NilWhenNothingObserved is the "we don't know"
// case. A pool the daemon could not read must come back absent, not as a
// zero-valued struct — the same rule host_load already follows, and the
// difference between an operator seeing "unknown" and wrongly seeing an
// isolated-looking default.
func TestBackendStorageFromPool_NilWhenNothingObserved(t *testing.T) {
	if got := backendStorageFromPool("default", ""); got != nil {
		t.Errorf("expected nil for an unread pool, got %+v", got)
	}
}

// TestContainerServer_ListBackends_PeerReportsStorage proves the fleet view
// actually carries a peer's storage property end to end.
