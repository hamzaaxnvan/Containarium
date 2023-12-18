package sshsession

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"sort"
	"strings"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// The SSH session shipper (#2415): tails the JSONL sink the sshpiperd plugin
// writes and ships each record to the audit chain of the backend the session
// was routed to, through AuditService.IngestSSHSessionRecords. See
// docs/architecture/sentinel-ssh-session-audit-ingest.md.

const (
	// DefaultBatchSize caps records per ingest call (the server's own cap).
	DefaultBatchSize = 500
	// DefaultOrphanAfter is how long an "open" with no "close" is tolerated
	// before --reconcile calls it an orphan. It is a heuristic: a genuinely
	// long-lived session older than this is reported as an orphan too, and
	// its real close (if it ever arrives) is still recorded separately.
	DefaultOrphanAfter = 24 * time.Hour

	tokenExpiryWarnWithin = 14 * 24 * time.Hour
	tokenExpiryWarnEvery  = 24 * time.Hour

	backoffInitial = time.Second
	backoffMax     = time.Minute
)

// ErrBatchRejected marks a batch the backend refused as invalid or
// unauthorized (4xx-class). Resending the same batch fails the same way, so
// the checkpoint does not advance and the error is loud, not retried-quietly.
var ErrBatchRejected = errors.New("backend rejected the batch")

// IngestClient is the slice of the typed client the shipper needs. Both
// internal/client transports satisfy it.
type IngestClient interface {
	IngestSSHSessionRecords(ctx context.Context, req *pb.IngestSSHSessionRecordsRequest) (*pb.IngestSSHSessionRecordsResponse, error)
}

// BackendResolver maps a record's login to the backend id it was routed to.
// *sentinel.KeyStore satisfies it.
type BackendResolver interface {
	BackendForLogin(login string) (backendID string, ok bool)
}

// TokenSource returns a backend's registered audit-ingest token. It is
// consulted on every pass, so a rotation takes effect without a restart.
type TokenSource interface {
	TokenFor(backendID string) (token string, ok bool, err error)
	// Backends lists every backend with a registered token, so near-expiry
	// warnings fire on every pass — including an idle one with nothing to
	// ship, which is exactly when a lapsing token would otherwise go unseen.
	Backends() ([]string, error)
}

// ClientFactory builds the ingest client for one backend and token.
type ClientFactory func(backendID, token string) (IngestClient, error)

// ShipConfig wires the shipper. Everything environment-specific is injected
// so the loop is testable with fakes and wrapped thinly by the CLI.
type ShipConfig struct {
	RecordsFile    string
	CheckpointFile string
	// RotatedFiles are where the previous sink may be found after a
	// rename-style rotation, newest first. Default: RecordsFile + ".1".
	RotatedFiles []string

	SentinelID string
	// BatchSize defaults to DefaultBatchSize.
	BatchSize int
	// DefaultBackend receives records whose login routes nowhere (e.g. the
	// user was deleted before the record shipped). Empty means such records
	// are skipped and counted.
	DefaultBackend string

	Resolver  BackendResolver
	Tokens    TokenSource
	NewClient ClientFactory
	// Rejected classifies a client error as a non-retryable rejection.
	// Nil treats every error as retryable.
	Rejected func(error) bool

	// OrphanAfter is Reconcile's threshold; it must be positive.
	OrphanAfter time.Duration

	// Test seams; production leaves them nil.
	Now   func() time.Time
	Logf  func(format string, args ...any)
	Sleep func(ctx context.Context, d time.Duration) error
}

func (c *ShipConfig) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *ShipConfig) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

func (c *ShipConfig) batchSize() int {
	if c.BatchSize > 0 {
		return c.BatchSize
	}
	return DefaultBatchSize
}

func (c *ShipConfig) rotatedFiles() []string {
	if len(c.RotatedFiles) > 0 {
		return c.RotatedFiles
	}
	return []string{c.RecordsFile + ".1"}
}

// ShipStats counts what a pass did. Skips are counted, never silent.
type ShipStats struct {
	Shipped            int // records accepted as new rows
	Duplicates         int // records the backend already had
	SkippedUnroutable  int // login routes to no backend and no default is set
	SkippedMalformed   int // line is not a valid record
	RotatedFileMissing int // the rotated file was gone; its unshipped tail is lost
	OrphansClosed      int // Reconcile: orphan closes sent
}

// ---- one backend's pending batch ------------------------------------------

type pendingRec struct {
	rec Record
	end int64 // byte offset just past this record's line
}

type backendRun struct {
	pending   []pendingRec
	confirmed int64 // offset after the last record confirmed shipped
	failed    bool
	err       error
	client    IngestClient
}

// sender ships batches to backends for the duration of one pass, caching
// each backend's client and recording failures without aborting the others.
type sender struct {
	cfg  *ShipConfig
	runs map[string]*backendRun
	st   *ShipStats
	ctx  context.Context
}

func newSender(ctx context.Context, cfg *ShipConfig, st *ShipStats) *sender {
	return &sender{cfg: cfg, runs: map[string]*backendRun{}, st: st, ctx: ctx}
}

func (s *sender) run(backend string, startOffset int64) *backendRun {
	r, ok := s.runs[backend]
	if !ok {
		r = &backendRun{confirmed: startOffset}
		s.runs[backend] = r
	}
	return r
}
