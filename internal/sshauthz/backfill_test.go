package sshauthz

import (
	"sort"
	"strings"
	"testing"
)

// fixtureHome lays out a /home tree with an owner, a collaborator, the
// sentinel's marked block, comments, blank lines and an unparseable line.
// It returns the root and the public keys a backfill must register.
func fixtureHome(t *testing.T) (root string, want map[string]string) {
	t.Helper()
	root = t.TempDir()
	aliceA, aliceB, bob, sentinel := testKey(t), testKey(t), testKey(t), testKey(t)
	writeHome(t, root, "alice", strings.Join([]string{
		"# managed by containarium",
		strings.TrimSpace(aliceA),
		"",
		`no-pty ` + strings.TrimSpace(aliceB),
		"",
		SentinelKeyMarker,
		strings.TrimSpace(sentinel),
		"",
	}, "\n"))
	writeHome(t, root, "alice-container-bob", strings.TrimSpace(bob)+"\nnot-a-key garbage\n")
	writeHome(t, root, "empty", "")
	return root, map[string]string{
		strings.TrimSpace(aliceA): "alice",
		strings.TrimSpace(aliceB): "alice",
		strings.TrimSpace(bob):    "alice-container-bob",
	}
}

func TestScanAuthorizedKeys(t *testing.T) {
	root, want := fixtureHome(t)
	res, err := ScanAuthorizedKeys(root, caseNow)
	if err != nil {
