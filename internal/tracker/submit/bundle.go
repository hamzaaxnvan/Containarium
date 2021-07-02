package submit

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// DefaultMaxBundleBytes bounds how large a bundle ExtractBundle will
// pull out of a box, per the design note's "size-capped, default
// 50 MiB". A run that produces more than this in new commits is almost
// certainly not what SubmitTrackerChange is for (binary assets,
// vendored dependencies committed by mistake, or a runaway agent).
const DefaultMaxBundleBytes = 50 * 1024 * 1024

var (
	// ErrBundleTooLarge is returned when the box reports a bundle file
	// larger than the caller's cap. ExtractBundle checks this via an
	// in-box `stat`/`wc -c` BEFORE pulling the file out, so an
	// oversized bundle costs one exec round trip, not a multi-MB
	// transfer that gets thrown away.
	ErrBundleTooLarge = errors.New("submit: bundle exceeds the configured size limit")
	// ErrBundleMalformed is returned when the bytes read back from the
	// box do not parse as a bundle (`git bundle list-heads` fails or
	// reports no heads). `git bundle create` in the box already
	// refuses to produce an empty bundle for an empty commit range —
	// this check exists for the box-to-daemon leg of the journey, not
	// for the box's own git catching a bad range.
	ErrBundleMalformed = errors.New("submit: bundle is malformed or contains no commits")
)

// BoxRunner is the narrow slice of *container.Manager's API
// ExtractBundle needs — one exec, one file read — so this package
// depends on no wider box-management surface and its tests need no
// real box. *container.Manager already satisfies this.
type BoxRunner interface {
	ExecWithOutput(containerName string, command []string) (stdout, stderr string, err error)
	ReadFile(containerName, path string) ([]byte, error)
}

// BundleResult is what a successful ExtractBundle produced: a bundle
// file already on the HOST filesystem (mode 0600, caller's
// responsibility to remove — GitPusher.PushBundle's caller does this
// the same way it cleans up its own temp dir) and the commit the
// bundle's HEAD ref resolves to.
type BundleResult struct {
	BundlePath string
	HeadSHA    string
}

// remoteBundlePath is the well-known in-box path ExtractBundle asks
// the workspace's own git to write the bundle to. A fixed name is
// fine: only one submit runs against a given workspace at a time (the
// run that owns it), and the file is removed from the box after being
// pulled out.
const remoteBundlePath = "/tmp/containarium-submit-out.bundle"

// shellSingleQuote wraps s in single quotes for safe interpolation
// into a /bin/sh -c script. Same technique as
// pkg/core/container/git_source.go's own helper of the same name;
// duplicated rather than imported because internal/tracker/submit must
