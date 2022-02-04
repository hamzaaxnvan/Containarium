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

