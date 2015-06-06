package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// #2560 slice A (#2880): an anchor sink outside the host.
//
// FileSink (anchor.go) defends against a Postgres-privileged attacker but
// not a host-root one, who can edit both the database and the file.
// GCSAnchorSink writes each anchored (checkpoint, root) to a GCS bucket
// whose retention policy is LOCKED (Bucket Lock): once written, an object
// cannot be deleted or overwritten by anyone — host root and the bucket's
// own admins included — until its retention period ends. That copy is what
// slice B's `audit verify-anchor` checks the database and the local file
// against.
//
// Like pkg/core/backup/gcs.go, it shells out to the host's `gcloud storage`
// rather than pulling in a cloud SDK; credentials are whatever the host's
// gcloud is configured with. One object per checkpoint, uploaded with
// --if-generation-match=0, so even an UNLOCKED bucket never has an anchor
// silently overwritten by this daemon.

// gcloudRunFunc runs `gcloud <args...>` with stdin and returns stdout. A
// non-nil error carries gcloud's stderr text. A field on the sink so tests
// substitute an in-memory bucket.
type gcloudRunFunc func(ctx context.Context, stdin []byte, args ...string) (string, error)

// gcloudCallTimeout bounds one gcloud invocation, so a hung network call
// cannot stall the anchor loop past its next tick.
const gcloudCallTimeout = 60 * time.Second

// gcloudWaitDelay is how long runGcloud waits, after the deadline kills the
// process group, for the output pipes to close before giving up on them.
const gcloudWaitDelay = 2 * time.Second

// GCSAnchorSink is a RootSink backed by objects in a GCS bucket.
type GCSAnchorSink struct {
	bucket string // bucket name, without gs://
	prefix string // "gs://<bucket>/<prefix>/", always ending in "/"
	run    gcloudRunFunc
}

// NewGCSAnchorSink builds a sink for uri ("gs://<bucket>" or
// "gs://<bucket>/<prefix>"), locating `gcloud` on PATH. It makes no network
// call; CheckRetentionLock is the startup check.
func NewGCSAnchorSink(uri string) (*GCSAnchorSink, error) {
	bin, err := exec.LookPath("gcloud")
	if err != nil {
		return nil, fmt.Errorf("audit: gcloud not found on PATH (required for the GCS anchor sink): %w", err)
	}
	run := func(ctx context.Context, stdin []byte, args ...string) (string, error) {
		return runGcloud(ctx, bin, gcloudCallTimeout, stdin, args...)
	}
	return newGCSAnchorSink(uri, run)
}

// runGcloud runs one `gcloud` invocation under a deadline.
func runGcloud(ctx context.Context, bin string, timeout time.Duration, stdin []byte, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// #nosec G204 -- bin is resolved via exec.LookPath("gcloud"); args are
	// fixed verbs plus gs:// URIs built from a validated bucket URI. No
	// shell is involved.
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	bindToProcessGroup(cmd)
	cmd.WaitDelay = gcloudWaitDelay
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func newGCSAnchorSink(uri string, run gcloudRunFunc) (*GCSAnchorSink, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(uri), "gs://")
	if !ok {
		return nil, fmt.Errorf("audit: anchor bucket must be a gs:// URI, got %q", uri)
	}
	bucket, prefix, _ := strings.Cut(rest, "/")
	if bucket == "" {
		return nil, fmt.Errorf("audit: anchor bucket URI %q has no bucket name", uri)
	}
	prefix = strings.Trim(prefix, "/")
	if !gcsBucketName.MatchString(bucket) {
		return nil, fmt.Errorf("audit: anchor bucket name %q is not a valid GCS bucket name", bucket)
	}
	// gcloud expands * ? [ in a gs:// URL; a wildcard prefix would make
	// LastPublished list something else and report "never anchored".
	if prefix != "" && (!gcsObjectPrefix.MatchString(prefix) || strings.Contains(prefix, "//") || slices.Contains(strings.Split(prefix, "/"), "..")) {
		return nil, fmt.Errorf("audit: anchor bucket prefix %q has characters gcloud would expand or is not a plain path", prefix)
	}
	p := "gs://" + bucket + "/"
	if prefix != "" {
		p += prefix + "/"
	}
	return &GCSAnchorSink{bucket: bucket, prefix: p, run: run}, nil
}

// URI is the sink's location, for log lines.
func (g *GCSAnchorSink) URI() string { return g.prefix }

// objectURI zero-pads the checkpoint (int64 fits in 19 digits) so object
// names sort in checkpoint order.
func (g *GCSAnchorSink) objectURI(checkpoint int64) string {
	return fmt.Sprintf("%s%020d.json", g.prefix, checkpoint)
}

func (g *GCSAnchorSink) PublishRoot(ctx context.Context, checkpoint int64, root string) error {
	rec := anchorRecord{Checkpoint: checkpoint, Root: root, AnchoredAt: time.Now().UTC().Format(time.RFC3339Nano)}
	body, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("audit: marshal anchor record: %w", err)
	}
	uri := g.objectURI(checkpoint)
	_, err = g.run(ctx, append(body, '\n'), "storage", "cp", "--if-generation-match=0", "-", uri)
	if err == nil {
		return nil
	}
	if !isGcloudPreconditionFailed(err) {
		return fmt.Errorf("audit: upload anchor %s: %w", uri, err)
	}
	// The object already exists: same root is an idempotent republish,
	// a different one is a conflict (RootSink contract).
	existing, err := g.readRecord(ctx, uri)
	if err != nil {
		return err
	}
	if existing.Root == root {
		return nil
	}
	return fmt.Errorf("%w: checkpoint=%d (%s)", ErrAnchorConflict, checkpoint, uri)
}

// LastPublished lists the anchor objects and reads the highest checkpoint.
// An empty prefix is ok=false; any other gcloud failure is an error, never
// "never anchored".
func (g *GCSAnchorSink) LastPublished(ctx context.Context) (int64, string, bool, error) {
	out, err := g.run(ctx, nil, "storage", "ls", g.prefix)
	if err != nil {
		if isGcloudNoMatch(err) {
			return 0, "", false, nil
		}
		return 0, "", false, fmt.Errorf("audit: list anchors under %s: %w", g.prefix, err)
	}
	var best int64 = -1
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, g.prefix) {
			continue
		}
		name, ok := strings.CutSuffix(path.Base(line), ".json")
		if !ok || len(name) != 20 {
			continue
		}
		cp, err := strconv.ParseInt(name, 10, 64)
		if err != nil || cp <= best {
			continue
		}
		best = cp
	}
	if best < 0 {
		return 0, "", false, nil
	}
	rec, err := g.readRecord(ctx, g.objectURI(best))
	if err != nil {
		return 0, "", false, err
	}
	return rec.Checkpoint, rec.Root, true, nil
}

func (g *GCSAnchorSink) readRecord(ctx context.Context, uri string) (anchorRecord, error) {
