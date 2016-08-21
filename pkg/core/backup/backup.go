// Package backup implements logical (pg_dump) database backups for the
// databases running inside Containarium containers, stored off the
// database host.
//
// Design (see docs/DB-BACKUP-OPERATIONS.md for the operator runbook and
// the ISO 27001 A.8.13 control mapping):
//
//   - The dump is produced by running pg_dump *inside* the tenant's
//     container (reaching the container's own Postgres over loopback),
//     writing a compressed custom-format archive to the container's /tmp.
//   - The archive is pulled to the daemon host (ReadFileStream), checksummed,
//     and either kept in the host backup directory (LOCAL) or shipped to
//     an object store and removed from local staging (GCS).
//   - Metadata is persisted as a small JSON sidecar per backup in the
//     host backup directory, so ListBackups works even when the database
//     being backed up is down — the index never shares a failure domain
//     with the data it describes.
//
// The package deliberately does not import the protobuf types; the
// server layer translates between these core types and pb.
package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/footprintai/containarium/pkg/core/secrets"
)

// EnginePostgres is the only database engine supported in v1.
const EnginePostgres = "postgres"

// Destination is where a dump is stored off-host. Kept as a string in the
// core so the package stays free of the pb dependency; the server maps it
// to/from pb.BackupDestination.
type Destination string

const (
	DestLocal Destination = "local"
	DestGCS   Destination = "gcs"
)

// KeyMode names who holds the key that opens an encrypted backup (#2402).
// Kept as a typed string in the core so the package stays free of the pb
// dependency; the server maps it to/from pb.BackupKeyMode.
//
//   - KeyModeAgeRecipient: encrypted to a tenant-held age recipient only;
//     the platform holds no key at all (#1831). Never written to the
//     sidecar — it is derived from Encrypted + AgeRecipient, so records
//     written before this type existed read the same (see
//     Record.EffectiveKeyMode).
//   - KeyModeManaged: encrypted to a per-backup ephemeral identity whose
//     secret is wrapped by the daemon's KMS wrapper and stored on the
//     record. Only a holder of the KMS decrypt role can open it.
//   - KeyModeBoth: both of the above in one age file; either path decrypts.
type KeyMode string

const (
	KeyModeAgeRecipient KeyMode = "age_recipient"
	KeyModeManaged      KeyMode = "managed"
	KeyModeBoth         KeyMode = "both"
)

// ContainerOps is the slice of *container.Manager the backup manager
// needs. Declared as an interface so tests can supply a fake without an
// Incus backend.
type ContainerOps interface {
	// Exec runs a command inside the container, discarding output.
	Exec(containerName string, command []string) error
	// ExecWithOutput runs a command inside the container and returns
	// stdout/stderr (pg_dump/pg_restore report errors on stderr).
	ExecWithOutput(containerName string, command []string) (string, string, error)
	// ReadFileStream returns a stream owned and closed by the caller.
	ReadFileStream(containerName, path string) (io.ReadCloser, error)
	// WriteFileStream consumes content through EOF, reporting read errors.
	WriteFileStream(containerName, path string, content io.Reader, mode string) error
}

// Uploader ships a staged local file to and from an off-host object
// store. The GCS implementation shells out to the host's `gcloud`; tests
// supply a fake.
type Uploader interface {
	Upload(localPath, destURI string) error
	DownloadStream(destURI string, dst io.Writer) error
	Delete(destURI string) error
}

// Record is the metadata index entry for one stored dump. Persisted as
// JSON in the host backup directory.
type Record struct {
	ID          string      `json:"id"`
	Username    string      `json:"username"`
	Database    string      `json:"database"`
	CreatedAt   time.Time   `json:"created_at"`
	SizeBytes   int64       `json:"size_bytes"`
	SHA256      string      `json:"sha256"`
	Destination Destination `json:"destination"`
	Location    string      `json:"location"`
	Engine      string      `json:"engine"`

	// LastVerification is the outcome of the most recent restore test,
	// nil until the backup has been verified. SHA256 proves the bytes
	// are intact; only this proves the dump is restorable (#1159).
	LastVerification *Verification `json:"last_verification,omitempty"`

	// RelationCount is the number of user relations in the source
	// database at dump time — the manifest a restore test compares the
	// restored schema against. Nil when no manifest was captured (a
	// backup taken before verification existed, or a source that could
	// not be queried).
	RelationCount *int64 `json:"relation_count,omitempty"`

	// Encrypted is true when the stored bytes are an age ciphertext (#1831).
	// SizeBytes and SHA256 describe the ciphertext — what is actually stored
	// — so the integrity gate in fetchDump is unchanged. AgeRecipient is the
	// public recipient the dump was encrypted to; it is a public key, safe to
	// record, and tells an operator which identity can restore it.
	Encrypted    bool   `json:"encrypted,omitempty"`
	AgeRecipient string `json:"age_recipient,omitempty"`
	// Hook is the in-tenant command that produced a hook backup (#1831);
	// empty for pg_dump backups.
	Hook string `json:"hook,omitempty"`

	// Managed key mode (#2402). WrappedKey is the per-backup age identity's
	// secret string, wrapped by the daemon's KMS wrapper; KEKID is the key
	// version the wrapper used (for GCP, the full CryptoKeyVersion resource
	// name, so destruction can be guarded per version). KeyMode is written
	// only for KeyModeManaged / KeyModeBoth — a recipient-only record keeps
	// today's sidecar byte for byte, and an old sidecar lacking all three
	// fields loads unchanged. The daemon never unwraps WrappedKey.
	WrappedKey []byte  `json:"wrapped_key,omitempty"`
	KEKID      string  `json:"kek_id,omitempty"`
	KeyMode    KeyMode `json:"key_mode,omitempty"`
	// HookFormat is what the hook declared it emits (#2405); empty for
	// pg_dump records. A hook record written before the field existed
	// loads as HookFormatOpaque.
	HookFormat HookFormat `json:"hook_format,omitempty"`
}

// EffectiveKeyMode is the key mode a reader should act on: the recorded
// one for managed records, KeyModeAgeRecipient for any other encrypted
// record (including every sidecar written before KeyMode existed), and
// "" for plaintext.
func (r *Record) EffectiveKeyMode() KeyMode {
	if r.KeyMode != "" {
		return r.KeyMode
	}
	if r.Encrypted && r.AgeRecipient != "" {
		return KeyModeAgeRecipient
	}
	return ""
}

// PgConn carries the connection parameters pg_dump / pg_restore use
// *inside the container*. Password is passed to the child via the
// PGPASSWORD environment variable, never on argv.
type PgConn struct {
	Database string
	User     string
	Password string
	Host     string
	Port     int
}

func (c PgConn) withDefaults() PgConn {
	if c.User == "" {
		c.User = "postgres"
	}
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if c.Port == 0 {
		c.Port = 5432
	}
	return c
}

// Manager orchestrates backups. One per daemon.
type Manager struct {
	ops          ContainerOps
	uploader     Uploader // may be nil → GCS destinations are rejected
	dir          string   // host backup directory (dumps for LOCAL + sidecar index for all)
	maxDumpBytes int64
	clock        func() time.Time

	// Whole-box backups (#2801). box is nil on a manager built without
	// WithBoxOps, and then CreateBox refuses. boxStagingDirs are the
	// directories Incus stages an export in before streaming it (counted
	// by the free-space pre-flight); maxBoxBytes is the BOX cap, separate
	// from maxDumpBytes; freeSpace is statDiskSpace outside tests.
	box            BoxOps
	boxStagingDirs []string
	maxBoxBytes    int64
	freeSpace      func(path string) (diskSpace, error)
	// boxClaims serializes backups per box and reserves their space (#3116).
	boxClaims boxClaims
	// wrapper, when set, wraps a per-backup age identity with a KMS
	// (#2402): backups default to KeyModeManaged (or KeyModeBoth when a
	// legacy recipient is also given). Nil keeps today's behaviour byte
	// for byte and refuses an explicit request for a managed mode.
	wrapper secrets.KMSClient
}

// Option configures a Manager at construction.
type Option func(*Manager)

// WithWrapper installs the KMS wrapper for managed key mode (#2402). The
// wrapper only ever needs the encrypt half of its key: the daemon wraps
// and never unwraps. A nil wrapper is the same as not calling this.
func WithWrapper(w secrets.KMSClient) Option {
	return func(m *Manager) { m.wrapper = w }
}

// NewManager constructs a backup manager. dir is the host directory where
// dumps (for LOCAL) and the JSON index (for all destinations) are kept.
// uploader may be nil when no object store is configured; GCS backups
// then return a clear error.
func NewManager(ops ContainerOps, uploader Uploader, dir string, opts ...Option) *Manager {
	m := &Manager{ops: ops, uploader: uploader, dir: dir, clock: time.Now, maxDumpBytes: DefaultMaxDumpBytes, maxBoxBytes: DefaultMaxBoxBytes, freeSpace: statDiskSpace}
	for _, o := range opts {
		o(m)
	}
	return m
}

// HasWrapper reports whether managed key mode is available on this daemon.
func (m *Manager) HasWrapper() bool { return m.wrapper != nil }

// CreateOptions parameterizes a backup.
type CreateOptions struct {
	Username      string
	ContainerName string
	Conn          PgConn
	Destination   Destination
	GCSBucket     string // e.g. "gs://my-backups/pg" — required for DestGCS

	// Hook, when set, is an absolute path to an executable INSIDE the
	// container whose stdout is the dump (#1831). The pg_dump path and Conn
	// are bypassed entirely: the hook reaches its database over localhost
	// under the container's own auth, so no credential crosses to the
	// platform. Label fills the record's "database" slot (defaults to the
	// hook's basename).
	Hook  string
	Label string
	// HookFormat declares what the hook emits (#2405). Empty means
	// HookFormatOpaque. Only valid together with Hook.
	HookFormat HookFormat
	// AgeRecipient, when set, encrypts the dump to this age recipient
	// ("age1…") in-process before it is staged or uploaded (#1831). The
	// platform and the storage backend then hold ciphertext only.
	AgeRecipient string
	// KeyMode selects who holds the key (#2402). Empty resolves to the
	// daemon default: KeyModeManaged when a wrapper is configured
	// (KeyModeBoth if AgeRecipient is also set), otherwise the #1831
	// behaviour (AgeRecipient or plaintext). An explicit mode the daemon
	// cannot satisfy — a managed mode with no wrapper, a recipient mode
	// with no recipient — is an error, never a silent downgrade.
	KeyMode KeyMode
}

// RestoreOptions parameterizes a restore.
type RestoreOptions struct {
	ID            string
	ContainerName string
	Conn          PgConn // Database empty → restore into the record's database
	Clean         bool   // pass --clean --if-exists to pg_restore
	// AgeIdentity ("AGE-SECRET-KEY-1…") decrypts an Encrypted record for
	// this one call. The platform holds no decryption key; the caller
	// supplies it and it is never stored or logged (#1831).
	AgeIdentity string
	// TargetContainer, when set, is the container restored into instead
	// of ContainerName (the backup's own container). A HookFormatPGCustom
	// hook record can only be restored this way, into a target other than
	// its source (#2405).
	TargetContainer string
}

func (m *Manager) now() time.Time {
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

// resolveKeyMode turns a request's key mode into the one Create will
// act on (#2402). "" is plaintext — only reachable with no wrapper and no
// recipient. Every refusal here happens before pg_dump runs, so a
// mis-specified request leaves nothing behind.
