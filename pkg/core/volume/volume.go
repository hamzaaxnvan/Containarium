// Package volume defines the runtime-neutral shared-volume service and the
// existing Incus/CephFS implementation (#384).
//
// The Incus Manager is capability-gated: create/attach are rejected unless
// the host has a `cephfs` Incus storage pool. The Kubernetes box backend
// implements Backend using an operator-selected RWX-capable CSI class.
//
// The Manager shells out to the `incus` CLI (the project's existing
// storage path). The command construction, CSV parsing, and capability
// detection are pure functions; the actual exec requires a live Ceph-backed
// Incus cluster.
package volume

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// ContentTypeFilesystem is the Incus content type of a shared volume.
const ContentTypeFilesystem = "filesystem"

// cephfsDriver is the Incus storage driver that supports concurrent RW.
const cephfsDriver = "cephfs"

// Runner executes `incus <args...>` and returns combined output. Injected
// so tests can supply a fake without a live cluster.
type Runner interface {
	Run(args ...string) (string, error)
}

// Attachment is one container a volume is mounted into.
type Attachment struct {
	Container string
	MountPath string
	ReadOnly  bool
}

// Volume is a CephFS custom filesystem volume.
type Volume struct {
	Name        string
	Pool        string
	SizeBytes   int64
	ContentType string
	Attachments []Attachment
}

// Backend is the runtime-neutral shared-volume service used by the gRPC
// surface. tenant scopes volumes on backends whose storage is namespaced.
type Backend interface {
	SharedVolumesSupported(ctx context.Context, tenant string) (pool string, ok bool, detail string)
	CreateVolume(ctx context.Context, tenant, name string, sizeBytes int64, pool string) (*Volume, error)
	ListVolumes(ctx context.Context, tenant, pool string) ([]Volume, error)
	GetVolume(ctx context.Context, tenant, name, pool string) (*Volume, error)
	DeleteVolume(ctx context.Context, tenant, name, pool string, force bool) error
	AttachVolume(ctx context.Context, tenant, name, pool, container, mountPath string, readOnly bool) error
	DetachVolume(ctx context.Context, tenant, name, container string) error
}

// Manager orchestrates volume lifecycle over the incus CLI.
type Manager struct {
	run Runner
}

// NewManager constructs a Manager.
func NewManager(r Runner) *Manager { return &Manager{run: r} }

// IncusBackend adapts the existing CephFS Manager to the runtime-neutral
// shared-volume service. Incus volumes are host-scoped, so tenant is ignored.
type IncusBackend struct{ Manager *Manager }

func (b IncusBackend) SharedVolumesSupported(_ context.Context, _ string) (string, bool, string) {
	return b.Manager.SharedVolumesSupported()
}

func (b IncusBackend) CreateVolume(_ context.Context, _ string, name string, sizeBytes int64, pool string) (*Volume, error) {
	return b.Manager.Create(name, sizeBytes, pool)
}

func (b IncusBackend) ListVolumes(_ context.Context, _ string, pool string) ([]Volume, error) {
	return b.Manager.List(pool)
}

func (b IncusBackend) GetVolume(_ context.Context, _ string, name, pool string) (*Volume, error) {
	return b.Manager.Get(name, pool)
}

func (b IncusBackend) DeleteVolume(_ context.Context, _ string, name, pool string, force bool) error {
	return b.Manager.Delete(name, pool, force)
}

func (b IncusBackend) AttachVolume(_ context.Context, _ string, name, pool, container, mountPath string, readOnly bool) error {
	return b.Manager.Attach(name, pool, container, mountPath, readOnly)
}

func (b IncusBackend) DetachVolume(_ context.Context, _ string, name, container string) error {
	return b.Manager.Detach(name, container)
}

// CephfsPools returns the names of cephfs-backed storage pools on this host.
func (m *Manager) CephfsPools() ([]string, error) {
	out, err := m.run.Run("storage", "list", "--format", "csv")
	if err != nil {
		return nil, fmt.Errorf("list storage pools: %w", err)
	}
	return parseCephfsPools(out), nil
}

// SharedVolumesSupported reports whether this host can serve shared volumes
// (a cephfs pool exists), returning the first such pool and a human detail.
func (m *Manager) SharedVolumesSupported() (pool string, ok bool, detail string) {
	pools, err := m.CephfsPools()
	if err != nil {
		return "", false, fmt.Sprintf("could not query storage pools: %v", err)
	}
	if len(pools) == 0 {
		return "", false, "no CephFS storage pool on this backend; shared (multi-writer) volumes require an Incus cluster on Ceph"
	}
	return pools[0], true, "CephFS pool: " + pools[0]
}
