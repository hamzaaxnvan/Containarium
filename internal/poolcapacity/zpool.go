package poolcapacity

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// ZpoolDeadline bounds one `zpool list`. A pool in trouble is exactly when
// zpool can hang, and the read must keep reporting Incus's own numbers then.
const ZpoolDeadline = 3 * time.Second

// CommandRunner runs a command and returns its stdout. ExecRunner in
// production; a stub in tests.
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecRunner runs name with args under ctx. A binary that is not installed
// comes back wrapping exec.ErrNotFound, which WithZpool treats as "this host
// has no ZFS tools", not as a failure.
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	// #nosec G204 -- the only caller is WithZpool: name is the fixed string
	// "zpool", the arguments are a fixed list plus a pool name that passed
	// validZpoolName (no leading '-', no shell, no path).
	out, err := exec.CommandContext(ctx, path, args...).Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		// zpool says why on stderr; keep the first line, capped, for the log.
		msg := strings.TrimSpace(string(ee.Stderr))
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		if len(msg) > 200 {
			msg = msg[:200]
		}
		if msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out, err
}

// validZpoolName is what ZFS allows in a pool name, minus anything that could
// read as an option or a path.
var validZpoolName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*$`)

// WithZpool wraps a Reader so each ZFS pool also carries its raw zpool numbers
// (#2853). Only pools with driver zfs and a derivable zpool name are asked;
// every other pool, and every pool whose zpool read fails, keeps exactly the
// reading Incus gave. The zpool reads run in parallel, each under deadline, so
// the added time is one deadline at worst, and a hung zpool never delays or
// fails the capacity read.
func WithZpool(read Reader, run CommandRunner, deadline time.Duration) Reader {
	return withZpool(read, run, deadline, log.Printf)
}

func withZpool(read Reader, run CommandRunner, deadline time.Duration, logf func(string, ...any)) Reader {
	if deadline <= 0 {
		deadline = ZpoolDeadline
