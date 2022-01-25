//go:build zfs

// Integration coverage for the container lifecycle hooks against a REAL
// ZFS pool (#1201, proof enabled by #1200).
//
// encryption_hooks_test.go drives these with a fake zfscrypt runner and
// proves the orchestration: what runs, in what order, what is tolerated.
// It cannot show that PostStop actually leaves a stopped container's
// dataset as ciphertext, which is the entire point of the hook.
//
//	sudo go test -tags=zfs ./internal/server/ -run TestIntegrationEncryption -v
//
// Only the Incus-side plumbing is faked here — the key-ref store and the
// dataset resolver. The KeyProvider, the zfscrypt Manager and the pool are
// all real.
package server

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/testsupport/zfspool"
	"github.com/footprintai/containarium/pkg/core/zfscrypt"
	"github.com/footprintai/containarium/pkg/core/zfskey"
)

// canary is the plaintext hunted for in the raw vdev.
var hookCanary = bytes.Repeat([]byte("CONTAINARIUM-HOOK-CANARY-4e1d8a6b-"), 64)

// memEncryptionState stands in for the Incus config keys the daemon really
// uses to record a container's key ref and storage pool.
type memEncryptionState struct {
	refs  map[string]zfskey.KeyRef
	pools map[string]string
}

func (m *memEncryptionState) GetKeyRef(name string) (zfskey.KeyRef, bool, error) {
	ref, ok := m.refs[name]
	return ref, ok, nil
}

func (m *memEncryptionState) SetKeyRef(name string, ref zfskey.KeyRef) error {
	m.refs[name] = ref
	return nil
}

func (m *memEncryptionState) GetPool(name string) (string, error) { return m.pools[name], nil }

func (m *memEncryptionState) SetPool(name, pool string) error {
	m.pools[name] = pool
	return nil
}

// memPools stands in for Incus's storage-pool API. Incus itself is not
// available in this lane — that is the incus lane's job (#1332) — so what is
// under test here is the ZFS half: that the datasets the hooks create really
// do give each tenant its own encryptionroot.
type memPools struct{ sources map[string]string }

func (m *memPools) CreateZFSPool(name, source string) error {
	m.sources[name] = source
	return nil
}

func (m *memPools) DeleteStoragePool(name string) error {
	delete(m.sources, name)
	return nil
}

func (m *memPools) StoragePoolSource(name string) (string, bool, error) {
	src, ok := m.sources[name]
	return src, ok, nil
}

// unavailableKeys stands in for key custody being unreachable.
type unavailableKeys struct{}

func (unavailableKeys) Wrap(context.Context, string) (zfskey.Key, zfskey.KeyRef, error) {
	return zfskey.Key{}, zfskey.KeyRef{}, errors.New("key custody unavailable")
}

func (unavailableKeys) Load(context.Context, zfskey.KeyRef) (zfskey.Key, error) {
	return zfskey.Key{}, errors.New("key custody unavailable")
}

// harness wires real encryption machinery over a throwaway pool.
type harness struct {
	pool  *zfspool.Pool
	hooks *encryptionHooks
	keys  *zfskey.FileKeyProvider
	refs  *memEncryptionState
	pools *memPools
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	zfspool.Require(t)

	keys, err := zfskey.NewFileKeyProvider(filepath.Join(t.TempDir(), "keys"))
	if err != nil {
		t.Fatalf("NewFileKeyProvider: %v", err)
	}
	h := &harness{
		pool:  zfspool.New(t),
		keys:  keys,
		refs:  &memEncryptionState{refs: map[string]zfskey.KeyRef{}, pools: map[string]string{}},
		pools: &memPools{sources: map[string]string{}},
	}
	h.hooks = &encryptionHooks{
		provider: keys,
		zfs:      zfscrypt.NewManager(nil), // the real zfs binary
		cache:    zfskey.NewCache(0),
		refs:     h.refs,
		pools:    h.pools,
		// Tenant encryptionroots are children of the throwaway pool, so
		// tenantDataset produces <pool>/<tenant> — the same shape production
		// gets from --zfs-tenant-root.
		tenantRoot: h.pool.Name,
	}
	return h
}

// createBox provisions a box through EnsureTenantStorage — the hook the
// create path calls — rather than reproducing its steps here.
//
// It used to mint the key, create the dataset and record the ref itself,
// which meant every test below exercised a local imitation of the create
// path instead of the path itself. A divergence between the two would have
// left these tests passing against code production does not run.
//
// The child dataset stands in for the one Incus would clone from the image
// INSIDE the tenant pool. Creating it with a plain `zfs create` is faithful
// to what matters here: it inherits the tenant's key exactly as a clone
// would, which is the property these tests exist to check.
func (h *harness) createBox(t *testing.T, ctx context.Context, container, tenant string) string {
	t.Helper()
	pool, ref, err := h.hooks.EnsureTenantStorage(ctx, tenant)
	if err != nil {
		t.Fatalf("EnsureTenantStorage(%s): %v", tenant, err)
	}

	dataset := h.pool.Dataset(tenant) + "/" + container
	zfspool.Run(t, "zfs", "create", dataset)

	if err := h.hooks.RecordPlacement(container, ref, pool); err != nil {
		t.Fatalf("RecordPlacement(%s): %v", container, err)
	}
	return dataset
}

// mountpointOf maps a dataset under the throwaway pool to its filesystem
// path. Derived rather than composed from the container name: a container's
// dataset now lives under its TENANT's encryptionroot, so the two are no
// longer the same string, and hardcoding the old shape is what made this
// test fail on the first run of the reshape.
func (h *harness) mountpointOf(dataset string) string {
	return filepath.Join(h.pool.Mount, strings.TrimPrefix(dataset, h.pool.Name+"/"))
}

// AC 1 (#1201): after stopping the last container under an encryptionroot,
// the key is unloaded and the dataset reads as ciphertext to host root.
//
// Driven through PostStop itself, not through zfscrypt.Manager — the hook
// is what production calls, and the hook is what has to leave the data
// unreadable.
func TestIntegrationEncryption_PostStopLeavesCiphertext(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	dataset := h.createBox(t, ctx, "box-alice", "alice")
	zfspool.Run(t, "zfs", "set", "compression=off", dataset)

	// Distinct prefixes, and the search uses the FULL prefixed string.
	// Searching for the bare shared suffix would find it inside the
	// control's plaintext — which sits unencrypted on this same vdev — and
	// report the encrypted dataset as leaking when it had not.
	encCanary := append([]byte("ENCRD-"), hookCanary...)
	secret := filepath.Join(h.mountpointOf(dataset), "secret.txt")
	if err := os.WriteFile(secret, encCanary, 0o600); err != nil {
		t.Fatalf("write canary: %v", err)
	}

	// Positive control. Without it, "the canary is absent" would prove
	// nothing: ZFS compresses by default, so an absent canary could just
	// mean lz4 rearranged the bytes, and this test would pass just as
	// happily against a dataset that was never encrypted. A sibling
	// UNENCRYPTED dataset, compression off, establishes that the search can
	// find plaintext at all.
	plain := h.pool.Dataset("plain-control")
	zfspool.Run(t, "zfs", "create", "-o", "compression=off", plain)
	plainCanary := append([]byte("CTRL-"), hookCanary...)
	if err := os.WriteFile(filepath.Join(h.pool.Mount, "plain-control", "secret.txt"), plainCanary, 0o600); err != nil {
		t.Fatalf("write control canary: %v", err)
	}
	zfspool.UnmountIfMounted(t, plain)
	if !h.pool.ContainsPlaintext(t, plainCanary) {
		t.Fatal("POSITIVE CONTROL FAILED: plaintext on an unencrypted dataset was not found in the " +
			"raw vdev, so the check below cannot detect plaintext and would pass for the wrong reason")
	}

	// "Stop the container": Incus unmounts, then the hook runs.
	//
	// BOTH the container's dataset and the tenant encryptionroot above it
	// are unmounted, because `zfs unload-key` refuses while anything under
	// the root is mounted — including the root itself. The old design had
	// them be the same dataset, so one unmount sufficed; they are now two.
	//
	// The harness mounts the tenant root only because zfspool creates the
	// throwaway pool with an inherited mountpoint. Whether a real Incus
	// leaves its pool's SOURCE dataset mounted is a different question, and
	// this lane has no Incus to answer it — see the note on #1341. If it
	// does, PostStop could never unload and "a stopped container is
	// ciphertext" would not hold in production.
	zfspool.UnmountIfMounted(t, dataset)
	zfspool.UnmountIfMounted(t, h.pool.Dataset("alice"))
	h.hooks.PostStop(ctx, "box-alice")

	status, err := h.hooks.zfs.KeyStatus(ctx, dataset)
	if err != nil {
		t.Fatalf("KeyStatus: %v", err)
	}
	if status != zfscrypt.KeyUnavailable {
		t.Errorf("keystatus after PostStop = %q, want %q — the hook did not drop the key",
			status, zfscrypt.KeyUnavailable)
	}

	if h.pool.ContainsPlaintext(t, encCanary) {
