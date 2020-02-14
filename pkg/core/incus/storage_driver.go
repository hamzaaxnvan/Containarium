package incus

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StorageDriver is an incus storage-pool driver. Typed rather than a bare
// string so the isolation classification below is attached to the value and
// callers cannot pass an arbitrary string where a driver is expected.
type StorageDriver string

const (
	StorageDriverZFS   StorageDriver = "zfs"
	StorageDriverBtrfs StorageDriver = "btrfs"
	StorageDriverLVM   StorageDriver = "lvm"
	StorageDriverCeph  StorageDriver = "ceph"
	StorageDriverDir   StorageDriver = "dir"
)

// StorageIsolation says whether a pool on a given driver hands each container
// its own filesystem, or puts them all on one shared filesystem.
//
// This is the distinction behind #1206. On the `dir` driver every tenant
// rootfs is a directory on one ext4 filesystem, and therefore shares one jbd2
// journal. ext4's default `data=ordered` mode writes back a transaction's
// dirty data while holding the journal lock, so one tenant's buffered writes
// block another tenant's fsync() — measured at 17 ms → 11,885 ms on a backend
// with three CI tenants, with the host and the physical device both idle.
// That is a tenant-isolation gap, not only a performance one: a tenant
// degrades its neighbours by writing normally, with no privilege and no
// misconfiguration.
//
// Drivers that allocate per-container volumes (a zfs dataset, a btrfs
// subvolume, an LVM logical volume, a ceph RBD image) have no shared journal
// for tenants to contend on.
type StorageIsolation int

const (
	// StorageIsolationUnknown is an unrecognised driver. Deliberately distinct
	// from "shared": we cannot vouch for it either way, and a policy that
	// requires isolation should refuse it rather than assume the best.
	StorageIsolationUnknown StorageIsolation = iota

	// StorageIsolationPerContainer means each container gets its own
	// dataset/subvolume/volume, so tenants do not share a filesystem journal.
	StorageIsolationPerContainer

	// StorageIsolationSharedFilesystem means every container's rootfs lives on
	// one filesystem — one journal, cross-tenant fsync stalls.
	StorageIsolationSharedFilesystem
)

func (i StorageIsolation) String() string {
	switch i {
	case StorageIsolationPerContainer:
		return "per-container-volume"
	case StorageIsolationSharedFilesystem:
		return "shared-filesystem"
	default:
		return "unknown"
	}
}

// Isolation classifies the driver. See StorageIsolation.
func (d StorageDriver) Isolation() StorageIsolation {
	switch d {
	case StorageDriverZFS, StorageDriverBtrfs, StorageDriverLVM, StorageDriverCeph:
		return StorageIsolationPerContainer
	case StorageDriverDir:
		return StorageIsolationSharedFilesystem
	default:
		return StorageIsolationUnknown
	}
}

const (
	// zfsContainersDataset is the ZFS dataset the installer creates. Its
	// presence is what the pre-existing autodetection keyed on.
	zfsContainersDataset = "incus-local/containers"

	// incusStoragePoolsRoot is where incus puts pool sources. Used both as the
	// btrfs probe target and as the btrfs pool source.
	incusStoragePoolsRoot = "/var/lib/incus/storage-pools"
)

// ErrSharedFilesystemStorage is returned when a pool would be provisioned on
// (or is already running on) a driver that does not isolate tenant volumes and
// the configured policy requires isolation. Wrapped, so callers can test with
// errors.Is.
var ErrSharedFilesystemStorage = errors.New("storage pool does not isolate tenant volumes")

// StorageProbe reports which per-container-volume backing stores this host can
// support. Injected rather than called directly so driver selection is
// testable without a real ZFS pool or btrfs filesystem — which matters here
// because no CI environment currently has either (see #1200).
type StorageProbe struct {
	// ZFSContainersDataset reports whether the installer's ZFS dataset exists.
	ZFSContainersDataset func() bool
