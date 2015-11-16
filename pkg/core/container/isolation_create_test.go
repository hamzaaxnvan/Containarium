package container

import (
	"strings"
	"testing"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// vmStagesBackend extends the stages fake with the one extra call the
// non-baked (VM) path makes: installPackages probes cloud-init through
// ExecWithExitCode. Exit 0 = present, so the (shortened) cloud-init wait
// path runs too.
type vmStagesBackend struct {
	*stagesBackend
}

func (b *vmStagesBackend) ExecWithExitCode(string, []string) (string, string, int, error) {
	return "", "", 0, nil
}

// TestCreate_IsolationVM_CreatesLinuxVM drives Create end to end against
// the stages fake and asserts what reaches Incus for a Linux VM request:
// instance type VM, the /cloud image variant, nesting and privileged
// podman forced off, and the baked-image fast path (a container image)
// not taken.
func TestCreate_IsolationVM_CreatesLinuxVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	b := &vmStagesBackend{stagesBackend: &stagesBackend{}}
	m := NewWithBackend(b)
	m.cloudInitWait = time.Millisecond

	opts := stagesOpts()
	opts.OSType = pb.OSType_OS_TYPE_UBUNTU_2404
	opts.Isolation = pb.IsolationType_ISOLATION_TYPE_VM
	opts.EnablePodman = true
	opts.EnablePodmanPrivileged = true

	if _, err := m.Create(opts); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := b.createdConfig
	if got.InstanceType != incusapi.InstanceTypeVM {
		t.Errorf("InstanceType = %q, want %q", got.InstanceType, incusapi.InstanceTypeVM)
	}
	if got.Image != "images:ubuntu/24.04/cloud" {
		t.Errorf("Image = %q, want the /cloud VM variant", got.Image)
	}
	if got.EnableNesting || got.EnablePodmanPrivileged {
		t.Errorf("nesting=%v privileged=%v: both must be off for a VM", got.EnableNesting, got.EnablePodmanPrivileged)
	}
	if got.Image == BakedImageAliasFor("images:ubuntu/24.04") {
		t.Errorf("Image = %q: the baked-image (container) fast path must not apply to a VM", got.Image)
