package tenantguard

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// The error a guard pass got on a real host when a failed create's rollback
// deleted the instance between the pass listing it and attaching its NIC
// (#2850).
var errDatasetGone = errors.New("ensure nic device eth0 on bob-container (operation failed): " +
	"Failed to write backup file: Failed to run: zfs set mountpoint=legacy canmount=noauto " +
	"tank/containers/bob-container: exit status 1 (cannot open " +
	"'tank/containers/bob-container': dataset does not exist)")

func fastGoneChecks(t *testing.T) {
	t.Helper()
	prevB, prevN := goneBackoff, goneChecks
	goneBackoff, goneChecks = time.Millisecond, 3
	t.Cleanup(func() { goneBackoff, goneChecks = prevB, prevN })
}

// removeFromHost makes the named instance vanish from ListContainers, the way
// a finished rollback does.
func removeFromHost(f *fakeBackend, name string) func(string) {
	return func(string) {
		kept := f.containers[:0:0]
