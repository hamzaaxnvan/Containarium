package backup

import (
	"archive/tar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
)

// The spill holds the (encrypted) content of excluded files, so it must not
// be readable by other users.
func TestBoxSpill_FileModeIs0600(t *testing.T) {
	s := &boxSpill{dir: t.TempDir()}
	defer s.close()
	if err := s.add("a", &tar.Header{Name: "a", Size: 1}, strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(s.f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("spill mode = %o, want 600", got)
	}
}

// A backup that fails after the spill was written deletes the spill.
func TestCreateBox_SpillDeletedOnErrorPath(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	good := buildArchive(t, incusLikeEntries())
	// The trailer is gone, so the failure comes after every entry, the
	// excluded files (and so the spill) included, has been read.
	box := newFakeBox(good[:len(good)-4])
	m := newBoxManager(t, box)
	if _, err := m.CreateBox(boxOpts(id.Recipient().String())); err == nil {
		t.Fatal("CreateBox succeeded on a truncated export")
	}
	assertNothingStaged(t, m.dir)
}

// ... and a successful one leaves only the finished archive and its sidecar.
func TestCreateBox_SpillDeletedOnSuccess(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	m := newBoxManager(t, newFakeBox(buildArchive(t, incusLikeEntries())))
	if _, err := m.CreateBox(boxOpts(id.Recipient().String())); err != nil {
		t.Fatal(err)
	}
	if left := spillFiles(t, m.dir); len(left) != 0 {
		t.Errorf("spill files left behind: %v", left)
	}
}

func spillFiles(t *testing.T, dir string) []string {
	t.Helper()
	got, err := filepath.Glob(filepath.Join(dir, ".box-spill-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func touchOld(t *testing.T, path string, age time.Duration) {
	t.Helper()
	old := time.Now().Add(-age)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// #3126: a SIGKILL mid-backup leaves its spill file behind. A sweep removes
// stale ones, and only those: exact name, regular file, old enough, not the
// spill of a backup running in this process, never through a symlink.
func TestSweepBoxSpills(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	writeFile(t, victim, "must survive")
	touchOld(t, victim, 72*time.Hour)

	stale := filepath.Join(dir, ".box-spill-111.tmp")
	fresh := filepath.Join(dir, ".box-spill-222.tmp")
	notSpill := filepath.Join(dir, "alice-box-20261027T000000Z.box.tar.gz.age")
	wrongExt := filepath.Join(dir, ".box-spill-333.txt")
	wrongPrefix := filepath.Join(dir, "box-spill-444.tmp")
	staleDirName := filepath.Join(dir, ".box-spill-555.tmp")
	link := filepath.Join(dir, ".box-spill-666.tmp")
	inFlight := filepath.Join(dir, ".box-spill-777.tmp")
	for _, p := range []string{stale, fresh, notSpill, wrongExt, wrongPrefix, inFlight} {
		writeFile(t, p, "x")
	}
	if err := os.Mkdir(staleDirName, 0o700); err != nil {
		t.Fatal(err)
	}
