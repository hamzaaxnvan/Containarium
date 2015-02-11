package backup

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"filippo.io/age"
)

// Whole-box backups (#2801, slice 2 of #2372). The box is exported through
// Incus (incus.Backend.ExportInstance, #3072), filtered and digested on the
// way through, encrypted like any other backup, and stored at the usual
// destination. See docs/architecture/container-snapshots.md, "Whole-box
// backups".

// EngineBox marks a whole-box record (BACKUP_ENGINE_BOX on the wire). Such a
// record is restored by importing it as a new box, never by pg_restore.
const EngineBox = "box"

// DefaultMaxBoxBytes is the BOX cap on the stored archive: 256 GiB, separate
// from the 16 GiB database-dump cap (decision of 2026-10-25 on #2801).
const DefaultMaxBoxBytes int64 = 256 << 30

// BoxManifestPath is the archive entry that carries the manifest. It is the
// last entry of the tarball, so it sits inside the encryption with the data
// it describes. A restore strips it before handing the archive to Incus.
const BoxManifestPath = "backup/containarium-manifest.json"

// boxArchiveExt names what a stored whole-box backup is: an age file
// wrapping a gzip tarball. A BOX backup is never plaintext.
const boxArchiveExt = ".box.tar.gz.age"

// boxRootfsPrefix is where an Incus instance backup keeps the root
// filesystem.
const boxRootfsPrefix = "backup/container/rootfs/"

// boxExcludedDirs are the rootfs directories whose contents a whole-box
// backup leaves out (decision of 2026-10-25 on #2800). The directory
// entries themselves are kept, so a restored box still has them with their
// modes.
var boxExcludedDirs = []string{"tmp", "var/tmp", "var/cache", "run"}

// ErrInsufficientSpace is the free-space pre-flight refusing a whole-box
// backup before it starts.
var ErrInsufficientSpace = errors.New("not enough free space for a whole-box backup")

// ErrBoxBackupInProgress means another whole-box backup of the same box is
// still running. Backups of one box are serialized: a second one is refused,
// not queued, so the caller sees it and retries (#3116).
var ErrBoxBackupInProgress = errors.New("a whole-box backup of this box is already in progress")

// ErrBoxUnavailable means this daemon cannot export a box at all.
var ErrBoxUnavailable = errors.New("whole-box backup is not available on this daemon (no Incus export)")

// BoxOps is what a whole-box backup needs from the container layer.
type BoxOps interface {
	// ExportInstance streams the instance as an Incus backup tarball. The
	// caller closes the reader.
	ExportInstance(containerName string) (io.ReadCloser, error)
	// BoxDiskUsage reports the bytes the box's root disk uses: the size
	// estimate the free-space pre-flight works from.
	BoxDiskUsage(containerName string) (int64, error)
}

// WithBoxOps enables whole-box backups.
func WithBoxOps(b BoxOps) Option {
	return func(m *Manager) { m.box = b }
}

// WithBoxStagingDirs names directories where Incus stages an export before
// streaming it (on Incus 6.0 LTS, its backups directory). The free-space
// pre-flight counts one copy of the box in each that exists.
func WithBoxStagingDirs(dirs ...string) Option {
	return func(m *Manager) { m.boxStagingDirs = append(m.boxStagingDirs, dirs...) }
}

// WithMaxBoxBytes overrides the 256 GiB BOX cap. Non-positive values keep
// the default; there is no unlimited mode.
func WithMaxBoxBytes(n int64) Option {
	return func(m *Manager) {
		if n > 0 {
			m.maxBoxBytes = n
		}
	}
}

// withFreeSpace replaces the statfs probe (tests).
func withFreeSpace(f func(path string) (diskSpace, error)) Option {
	return func(m *Manager) { m.freeSpace = f }
}

// diskSpace is what the pre-flight needs to know about a directory: free
// bytes for an unprivileged writer, and which filesystem it is on, so two
// staging areas on one filesystem are counted together.
type diskSpace struct {
	Free   uint64
	Device uint64
}

// BoxCreateOptions parameterizes a whole-box backup. The fields mean what
// they mean in CreateOptions, except that KeyMode is required: a BOX
// backup is never plaintext.
type BoxCreateOptions struct {
	Username      string
	ContainerName string
	Destination   Destination
	GCSBucket     string
	AgeRecipient  string
	KeyMode       KeyMode
}

// BoxManifest is written at create time as the archive's last entry
// (decision of 2026-10-25 on #2801), for verify-by-restore (#2802) to
// compare a restored box against.
type BoxManifest struct {
	Version   int       `json:"version"`
	Box       string    `json:"box"`
	CreatedAt time.Time `json:"created_at"`
	// Excluded lists the directories whose contents were left out, as
	// absolute paths inside the box.
	Excluded []string `json:"excluded"`
	// Files are the regular files of the root filesystem, by absolute path
	// inside the box.
	Files []BoxManifestFile `json:"files"`
	// Links are the hard links of the root filesystem.
	Links []BoxManifestLink `json:"links,omitempty"`
	// Config are the regular files outside the root filesystem, by archive
	// path: Incus's index.yaml, and backup.yaml, which carries the box's
	// config and devices.
	Config []BoxManifestFile `json:"config"`
}

// BoxManifestFile is one regular file and its SHA-256.
type BoxManifestFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// BoxManifestLink is one hard link; Target is the file it shares content
// with.
type BoxManifestLink struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

// boxManifestVersion is the manifest format version.
const boxManifestVersion = 1

func errBoxRecordNotDatabase(id, verb string) error {
	return fmt.Errorf("backup %s is a whole-box backup: it cannot be %s through the database path (whole-box restore is tracked in #2801)", id, verb)
}

// CreateBox backs up a whole box: export, drop the excluded directories'
// contents, digest every regular file into the manifest, encrypt, and
// store. Every refusal precedes the export.
func (m *Manager) CreateBox(opts BoxCreateOptions) (*Record, error) {
	if m.box == nil {
		return nil, ErrBoxUnavailable
	}
	if opts.ContainerName == "" {
		return nil, fmt.Errorf("container name is required")
	}
	if opts.KeyMode == "" {
		return nil, fmt.Errorf("a whole-box backup cannot be plaintext: name a key mode (managed, age_recipient or both)")
	}
	if opts.AgeRecipient != "" {
		if _, err := parseRecipient(opts.AgeRecipient); err != nil {
			return nil, err
		}
	}
	mode, err := m.resolveKeyMode(opts.KeyMode, opts.AgeRecipient)
	if err != nil {
		return nil, err
	}
	if err := m.checkDestination(opts.Destination, opts.GCSBucket); err != nil {
		return nil, err
	}
	id := fmt.Sprintf("%s-box-%s", opts.Username, m.now().UTC().Format("20060102T150405Z"))
	if err := validateBackupID(id); err != nil {
		return nil, fmt.Errorf("cannot derive a safe backup id: %w", err)
	}
	if _, err := os.Stat(m.sidecarPath(id)); err == nil {
		return nil, fmt.Errorf("backup %s already exists: retry in a second", id)
	}
	// Leftovers of a daemon that died mid-backup would count against the
	// free space the pre-flight is about to read.
	if _, err := m.SweepStaleBoxSpills(); err != nil {
		log.Printf("[backup] sweeping stale whole-box spill files: %v", err)
	}
	release, err := m.boxPreflight(opts.ContainerName)
	if err != nil {
		return nil, err
	}
	// The claim and the space it reserved last until the backup is stored
	// or has failed.
	defer release()
	plan, err := m.planEncryption(mode, opts.AgeRecipient)
	if err != nil {
		return nil, err
	}

	export, err := m.box.ExportInstance(opts.ContainerName)
	if err != nil {
		return nil, fmt.Errorf("export %s: %w", opts.ContainerName, err)
	}
	// The filter runs in its own goroutine, writing into a pipe that
	// stageDump reads from. Closing the read end on a staging failure
	// stops the filter; waiting on done keeps the export open until the
	// filter is off it.
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := filterBoxArchive(export, pw, opts.ContainerName, m.now().UTC(), m.maxBoxBytes, m.dir)
		pw.CloseWithError(err)
		done <- err
	}()
	localPath, size, checksum, stageErr := m.stageDump(pr, id, boxArchiveExt, plan.recipients, m.maxBoxBytes)
	// Unblock the filter whichever side stopped first: closing the read end
	// fails its next write, and closing the export fails its next read.
	_ = pr.CloseWithError(errors.New("staging stopped"))
	closeErr := export.Close()
	filterErr := <-done
	if stageErr != nil {
		// The filter's own error is the cause unless staging hit the cap,
		// in which case the filter only saw the pipe close.
		if filterErr != nil && !errors.Is(stageErr, ErrDumpTooLarge) {
			return nil, fmt.Errorf("export %s: %w", opts.ContainerName, filterErr)
		}
		return nil, fmt.Errorf("whole-box backup of %s: %w", opts.ContainerName, stageErr)
	}
	// Staging read the filter's output to EOF, so the filter succeeded: any
	// filter error would have reached stageDump through the pipe. A failure
	// to close the export (on Incus 6.0 LTS, to delete the stored backup it
	// streamed from) does not make the archive incomplete; Incus expires
	// that backup on its own.
	if closeErr != nil {
		log.Printf("[backup] whole-box backup %s: closing the export of %s: %v", id, opts.ContainerName, closeErr)
	}

	record := &Record{
		ID:          id,
		Username:    opts.Username,
		Database:    opts.ContainerName,
		CreatedAt:   m.now().UTC(),
		SizeBytes:   size,
		SHA256:      checksum,
		Destination: opts.Destination,
		Engine:      EngineBox,
	}
	plan.apply(record)
	if err := m.commit(record, localPath, opts.GCSBucket, boxArchiveExt); err != nil {
		return nil, err
	}
	return record, nil
}

// boxClaims is the daemon's record of whole-box backups in flight: which
// boxes, and how many bytes each reserved on each filesystem. The pre-flight
// reads the free space and takes its claim under one lock, so two backups
// cannot both pass the same reading (#3116). It is process-local: a hung
// export holds its claim and reservation until the daemon restarts.
type boxClaims struct {
	mu       sync.Mutex
	inFlight map[string]*boxClaim // box -> its claim
}

// boxClaim is one backup's claim: its reservation per device.
type boxClaim struct {
	box  string
	need map[uint64]uint64
}

// held is what running backups hold on dev: the sum of their full
// reservations. Call with mu held.
func (c *boxClaims) held(dev uint64) uint64 {
	var n uint64
	for _, cl := range c.inFlight {
		n += cl.need[dev]
	}
	return n
}

// boxPreflight refuses a backup that cannot fit before the export starts,
// and claims the box and the space for the caller. The returned release
