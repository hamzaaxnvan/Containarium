package container

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// withTestHomeRoot overrides the package's home root + user-exists check
// for a single test. Restores the originals via t.Cleanup so tests run
// in any order without bleeding state.
func withTestHomeRoot(t *testing.T, homeRoot string, userExistsFn func(string) bool) {
	t.Helper()
	origHome := authorizedKeysHomeRoot
	origExists := authorizedKeysUserExists
	authorizedKeysHomeRoot = homeRoot
	authorizedKeysUserExists = userExistsFn
	origChown := authorizedKeysChown
	authorizedKeysChown = func(string, string) error { return nil }
	t.Cleanup(func() {
		authorizedKeysChown = origChown
		authorizedKeysHomeRoot = origHome
		authorizedKeysUserExists = origExists
	})
}

const validTestKey1 = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBZkMdKTk8EXlTr5tlsIfAvlCi2iCl0YB/YDua3uMyDX test1"
const validTestKey2 = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDtmr5hyCwDmlxelT+dTGxmh8SpOObOWJIhoRa61oY2Q test2"

// TestJumpServerAuthorizesAllRequestKeys_470 is the #470 regression.
//
// The manager seeds the HOST jump-server authorized_keys with SSHKeys[0]
// only (CreateJumpServerAccount → setupUserSSHKey OVERWRITES the host file
// with a single key), then must AddAuthorizedKey the rest. Without that
// loop, any key after the first lands only in the CONTAINER-internal
// authorized_keys, never the host file the sentinel syncs from — so a
// client using e.g. an automation key that sorts after a registered key is
// rejected at the sentinel (publickey) though the box itself would accept it.
//
// This test exercises the host-side invariant the fix relies on: after the
// seed-then-authorize-the-rest sequence, EVERY supplied key is present in
// the host authorized_keys.
func TestJumpServerAuthorizesAllRequestKeys_470(t *testing.T) {
	tmp := t.TempDir()
	withTestHomeRoot(t, tmp, func(string) bool { return true })

	const user = "boxuser"
	keys := []string{validTestKey1, validTestKey2}

	// Simulate CreateJumpServerAccount seeding the host file with keys[0]
	// (setupUserSSHKey writes exactly one key, overwriting).
	akPath := filepath.Join(tmp, user, ".ssh", "authorized_keys")
	if err := os.MkdirAll(filepath.Dir(akPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(akPath, []byte(keys[0]+"\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// The fix: authorize the remaining keys host-side.
	for _, k := range keys[1:] {
		if err := AddAuthorizedKey(user, k); err != nil {
			t.Fatalf("AddAuthorizedKey(%q): %v", k, err)
		}
	}

	content, err := os.ReadFile(akPath)
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	for i, k := range keys {
		if !strings.Contains(string(content), k) {
			t.Errorf("host authorized_keys missing key[%d] — the sentinel would reject a client using it; #470 regression:\nkey:  %s\nfile:\n%s", i, k, content)
		}
	}
}

func TestAddAuthorizedKey_CreatesDirAndFile(t *testing.T) {
	tmp := t.TempDir()
	withTestHomeRoot(t, tmp, func(string) bool { return true })
