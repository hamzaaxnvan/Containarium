package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	mrand "math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"filippo.io/age"
)

// --- #2801 PR B: the whole-box create path ---

// tarEntry is one entry of a synthetic Incus instance backup tarball.
type tarEntry struct {
	name     string
	typ      byte
	body     string
	linkname string
	pax      map[string]string
}

// incusLikeEntries mirrors the layout the lane recorded for a real Incus 6.0
// export (docs/architecture/container-snapshots.md, "Whole-box export"):
// index and config at the top, the root filesystem under
// backup/container/rootfs/. It carries one probe in each excluded directory
// and the edge cases the filter has to get right.
func incusLikeEntries() []tarEntry {
	return []tarEntry{
		{name: "backup/", typ: tar.TypeDir},
		{name: "backup/index.yaml", typ: tar.TypeReg, body: "name: alice-container\nbackend: zfs\n"},
		{name: "backup/container/", typ: tar.TypeDir},
		{name: "backup/container/backup.yaml", typ: tar.TypeReg, body: "container:\n  name: alice-container\n  devices: {}\n"},
		{name: "backup/container/rootfs/", typ: tar.TypeDir},
		{name: "backup/container/rootfs/etc/", typ: tar.TypeDir},
		{name: "backup/container/rootfs/etc/hostname", typ: tar.TypeReg, body: "alice\n"},
		// File capabilities ride as PAX xattr records: dropping them would
		// break ping and friends in the restored box.
		{name: "backup/container/rootfs/usr/bin/ping", typ: tar.TypeReg, body: "ELF-ping", pax: map[string]string{"SCHILY.xattr.security.capability": "\x01\x00\x00\x02"}},
		{name: "backup/container/rootfs/usr/bin/vi", typ: tar.TypeLink, linkname: "backup/container/rootfs/usr/bin/ping"},
		{name: "backup/container/rootfs/var/run", typ: tar.TypeSymlink, linkname: "/run"},
		// Excluded directories: the directory entry stays, its contents go.
		{name: "backup/container/rootfs/tmp/", typ: tar.TypeDir},
		{name: "backup/container/rootfs/tmp/probe", typ: tar.TypeReg, body: "tmp probe"},
		{name: "backup/container/rootfs/tmp/sub/", typ: tar.TypeDir},
		{name: "backup/container/rootfs/var/tmp/", typ: tar.TypeDir},
		{name: "backup/container/rootfs/var/tmp/probe", typ: tar.TypeReg, body: "var-tmp probe"},
		{name: "backup/container/rootfs/var/cache/", typ: tar.TypeDir},
		{name: "backup/container/rootfs/var/cache/apt/pkgcache.bin", typ: tar.TypeReg, body: "cache"},
		{name: "backup/container/rootfs/run/", typ: tar.TypeDir},
		{name: "backup/container/rootfs/run/probe", typ: tar.TypeReg, body: "run probe"},
		// A file with one name in an excluded directory and another outside
		// it. The tar carries the content under the first name (here the
		// excluded one) and makes the kept name a hard link to it. The kept
		// name must keep the content: it becomes a regular file, and a
		// second kept link then points at it.
		{name: "backup/container/rootfs/root/tmp-link", typ: tar.TypeLink, linkname: "backup/container/rootfs/tmp/probe"},
		{name: "backup/container/rootfs/srv/second-link", typ: tar.TypeLink, linkname: "./backup/container/rootfs/tmp/probe"},
		// A link inside an excluded directory goes, whatever it points at.
		{name: "backup/container/rootfs/tmp/link-to-kept", typ: tar.TypeLink, linkname: "backup/container/rootfs/etc/hostname"},
		// Look-alikes that are NOT excluded.
		{name: "backup/container/rootfs/var/cachefile", typ: tar.TypeReg, body: "not a cache dir"},
		{name: "backup/container/rootfs/home/alice/tmp/notes", typ: tar.TypeReg, body: "a home tmp dir is data"},
	}
}

func buildArchive(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o644, Linkname: e.linkname, PAXRecords: e.pax}
		if e.typ == tar.TypeDir {
			h.Mode = 0o1777
		}
		if e.typ == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatalf("write header %s: %v", e.name, err)
		}
		if e.typ == tar.TypeReg {
			if _, err := io.WriteString(tw, e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fakeBox is the Incus side of a whole-box backup.
type fakeBox struct {
	archive   func() io.Reader
	usage     int64
	usageErr  error
	exportErr error
	exported  []string
	closed    int
}

func (f *fakeBox) ExportInstance(name string) (io.ReadCloser, error) {
	f.exported = append(f.exported, name)
	if f.exportErr != nil {
		return nil, f.exportErr
	}
	return ownedReader{Reader: f.archive(), close: func() { f.closed++ }}, nil
}

func (f *fakeBox) BoxDiskUsage(string) (int64, error) { return f.usage, f.usageErr }

func newFakeBox(archive []byte) *fakeBox {
	return &fakeBox{archive: func() io.Reader { return bytes.NewReader(archive) }, usage: 1 << 20}
}

func newBoxManager(t *testing.T, box BoxOps, opts ...Option) *Manager {
	t.Helper()
	m := NewManager(newFakeOps(nil), nil, t.TempDir(), append([]Option{WithBoxOps(box)}, opts...)...)
	m.clock = fixedClock
	return m
}

func boxOpts(recipient string) BoxCreateOptions {
	return BoxCreateOptions{
		Username:      "alice",
		ContainerName: "alice-container",
		Destination:   DestLocal,
		AgeRecipient:  recipient,
		KeyMode:       KeyModeAgeRecipient,
	}
}

// openBoxBackup decrypts a stored whole-box backup and returns its entries
// (name -> header, body) in archive order.
func openBoxBackup(t *testing.T, path, identity string) ([]string, map[string]*tar.Header, map[string][]byte) {
	t.Helper()
	ct, err := os.ReadFile(path) // #nosec G304 -- test temp file
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(ct, []byte("age-encryption.org/")) || bytes.Contains(ct, []byte("alice-container")) {
		t.Fatal("stored whole-box backup is not an age ciphertext")
	}
	pt, err := decryptWithIdentity(ct, identity)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(pt))
	if err != nil {
		t.Fatalf("stored plaintext is not gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	var order []string
	hdrs := map[string]*tar.Header{}
	bodies := map[string][]byte{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		order = append(order, h.Name)
		hdrs[h.Name], bodies[h.Name] = h, b
	}
	return order, hdrs, bodies
}

func sha(s string) string {
	d := sha256.Sum256([]byte(s))
	return hex.EncodeToString(d[:])
}

// The create path end to end: export, exclude, digest, encrypt, record.
func TestCreateBox_FiltersExcludesEncryptsAndWritesManifest(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
