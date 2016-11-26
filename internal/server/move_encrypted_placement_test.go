package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/zfskey"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Placement for a migrated encrypted container (#1203b, the half #1360 left).
//
// #1360 made the destination provision the tenant's pool and hand back its
// name. Nothing consumed it: `incus copy` still ran with no `--storage`, so
// the container landed on the destination's DEFAULT pool — outside the
// tenant's encryptionroot, unencrypted — while the pre-flight had just said
// the migration was safe. That combination is worse than no pre-flight at
// all, because the reassuring signal is the false one.
//
// These tests pin the copy going where the pre-flight said, and the
// destination recording enough to unlock it again afterwards.

// preflightOK installs a destination that accepts and names a pool.
func preflightOK(t *testing.T, pool string) {
	t.Helper()
	prev := overridePrepareForTest(func(*PeerClient, string, *pb.PrepareEncryptedMigrationRequest) (*pb.PrepareEncryptedMigrationResponse, error) {
		return &pb.PrepareEncryptedMigrationResponse{CanResolve: true, StoragePool: pool}, nil
	})
	t.Cleanup(prev)
}

// Every copy in the migration must target the tenant pool — not just the
// first. The final cutover copy is the one that carries the last delta, so a
// copy that forgot the pool there would leave the container's freshest state
// on the wrong dataset.
func TestMoveContainer_EveryCopyTargetsTheTenantPool(t *testing.T) {
	runner := &fakeRunner{hasRemote: true}
	cs := encryptedServer(t, runner)
	preflightOK(t, "containarium-tenant-alice")

	var adopted *pb.AdoptMigratedContainerRequest
	prev := overrideAdoptForTest(func(_ *PeerClient, _ string, req *pb.AdoptMigratedContainerRequest) (*pb.AdoptMigratedContainerResponse, error) {
		adopted = req
		return &pb.AdoptMigratedContainerResponse{NewIpAddress: "10.0.7.42"}, nil
	})
	defer prev()

	if _, err := cs.MoveContainer(adminCtx(), &pb.MoveContainerRequest{
		Username: "alice", TargetBackendId: "vm2",
	}); err != nil {
		t.Fatalf("MoveContainer: %v", err)
	}

	copies := 0
	for _, call := range runner.calls {
		if !strings.HasPrefix(call, "copyinit ") && !strings.HasPrefix(call, "refresh ") {
			continue
		}
		copies++
		if !strings.Contains(call, "storage=containarium-tenant-alice") {
			t.Errorf("copy %q did not target the tenant pool — the container lands on the "+
				"destination's default pool, outside its encryptionroot, and arrives unencrypted "+
				"while the pre-flight said the migration was safe", call)
		}
	}
	if copies == 0 {
		t.Fatal("no copies happened at all")
	}

	// And the destination must be told what it received, or it cannot
	// unlock the container on any later start.
	if adopted == nil {
		t.Fatal("adopt was never called")
	}
	if adopted.ZfsPool != "containarium-tenant-alice" {
		t.Errorf("adopt request pool = %q, want the tenant pool", adopted.ZfsPool)
	}
	var ref zfskey.KeyRef
	if err := json.Unmarshal([]byte(adopted.ZfsKeyRef), &ref); err != nil {
		t.Fatalf("adopt request carries no decodable key ref: %v", err)
	}
	if ref.URI != "/keys/alice.key" {
		t.Errorf("adopt ref = %+v, want alice's", ref)
	}
}

// The unencrypted path must be byte-identical: no --storage, no placement
// fields. Every container today takes this path.
func TestMoveContainer_UnencryptedCopiesCarryNoStorageOverride(t *testing.T) {
	runner := &fakeRunner{hasRemote: true}
	cs := newTestContainerServer(t, runner, "vm2")

	var adopted *pb.AdoptMigratedContainerRequest
	prev := overrideAdoptForTest(func(_ *PeerClient, _ string, req *pb.AdoptMigratedContainerRequest) (*pb.AdoptMigratedContainerResponse, error) {
		adopted = req
		return &pb.AdoptMigratedContainerResponse{NewIpAddress: "10.0.7.42"}, nil
	})
	defer prev()

	if _, err := cs.MoveContainer(adminCtx(), &pb.MoveContainerRequest{
		Username: "bob", TargetBackendId: "vm2",
	}); err != nil {
		t.Fatalf("MoveContainer: %v", err)
	}

	for _, call := range runner.calls {
		if strings.Contains(call, "storage=") {
