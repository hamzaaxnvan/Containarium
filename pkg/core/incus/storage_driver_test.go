package incus

import (
	"errors"
	"strings"
	"testing"
)

// TestStorageDriver_Isolation pins the property that actually matters for
// tenant isolation: does a pool on this driver give every container its own
// filesystem, or do they all share one?
//
// This is the classification #1206 turns on. A `dir` pool puts every tenant
// rootfs on one ext4 filesystem, so they share one jbd2 journal and one
// tenant's writeback stalls another's fsync. zfs/btrfs/lvm/ceph each hand a
// container its own dataset / subvolume / logical volume, so there is no
// shared journal to contend on.
func TestStorageDriver_Isolation(t *testing.T) {
	tests := []struct {
		name   string
		driver StorageDriver
		want   StorageIsolation
	}{
		{
			name:   "dir shares one filesystem across every tenant",
			driver: StorageDriverDir,
			want:   StorageIsolationSharedFilesystem,
		},
		{
			name:   "zfs gives each container its own dataset",
			driver: StorageDriverZFS,
			want:   StorageIsolationPerContainer,
		},
		{
			name:   "btrfs gives each container its own subvolume",
			driver: StorageDriverBtrfs,
			want:   StorageIsolationPerContainer,
		},
		{
			name:   "lvm gives each container its own logical volume",
			driver: StorageDriverLVM,
			want:   StorageIsolationPerContainer,
		},
		{
			name:   "ceph gives each container its own RBD image",
			driver: StorageDriverCeph,
			want:   StorageIsolationPerContainer,
		},
		{
			name:   "an unrecognised driver is unknown, not assumed safe",
			driver: StorageDriver("some-future-driver"),
			want:   StorageIsolationUnknown,
		},
		{
			name:   "the empty driver is unknown",
			driver: StorageDriver(""),
			want:   StorageIsolationUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.driver.Isolation(); got != tt.want {
				t.Errorf("StorageDriver(%q).Isolation() = %v, want %v", tt.driver, got, tt.want)
			}
		})
	}
}

// TestSelectStorageDriver_PrefersPerContainerVolumes is the core of #1206's
// suggested direction (1): when provisioning a pool, pick the most isolating
// driver the host can actually support, and only land on `dir` when there is
// nothing better.
//
// btrfs sits between zfs and dir deliberately: it is in-tree, so a long-lived
// backend does not need a DKMS module rebuilt across kernel upgrades, but it
// still gives every container its own subvolume.
func TestSelectStorageDriver_PrefersPerContainerVolumes(t *testing.T) {
	tests := []struct {
		name       string
		zfsDataset bool
		btrfsPath  bool
		wantDriver StorageDriver
		wantSource string
	}{
		{
			name:       "zfs dataset present wins",
			zfsDataset: true,
			btrfsPath:  false,
			wantDriver: StorageDriverZFS,
			wantSource: zfsContainersDataset,
		},
		{
			name:       "zfs still wins when btrfs is also available",
			zfsDataset: true,
			btrfsPath:  true,
			wantDriver: StorageDriverZFS,
			wantSource: zfsContainersDataset,
		},
		{
			name:       "btrfs is chosen when there is no zfs dataset",
			zfsDataset: false,
			btrfsPath:  true,
			wantDriver: StorageDriverBtrfs,
			wantSource: incusStoragePoolsRoot,
		},
		{
			name:       "dir only when nothing isolating is available",
			zfsDataset: false,
			btrfsPath:  false,
			wantDriver: StorageDriverDir,
			wantSource: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SelectStorageDriver(StorageProbe{
