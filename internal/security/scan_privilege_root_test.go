//go:build linux && scanroot

package security

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #2888: clamscan pre-checks files with access(2), which ignores
// CAP_DAC_READ_SEARCH when the REAL uid is not 0. This runs the real privilege
// arguments against a real root-only file, with `test -r` standing in for
// clamscan (dash's `test -r` is access(2)).
//
// It needs root and setpriv, so it is behind the `scanroot` build tag and is not
// part of any CI lane (a lane that fails on a skipped test must not carry a test
// that skips off-root). Run it deliberately, as root, on a Linux host:
//
//	sudo -E go test -tags scanroot -run TestScanPrivilegeArgs ./internal/security/
//
// The same check was also done by hand on a real box, see the PR.
func TestScanPrivilegeArgs_AccessCheckSeesARootOnlyFile(t *testing.T) {
	if os.Geteuid() != 0 {
