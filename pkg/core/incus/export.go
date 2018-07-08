package incus

import (
	"errors"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"time"

	incusclient "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
)

// Whole-box export/import (#2801): the seam a whole-box backup streams
// through. See docs/architecture/container-snapshots.md, "Whole-box export".

// ImportOptions are the overrides an import applies on top of the archived
// instance config.
type ImportOptions struct {
	// Config overrides instance config keys (key -> value). Neither may
	// contain whitespace: Incus carries the overrides in one
	// space-separated header.
	Config map[string]string
}

// exportBackupTTL bounds how long a stored backup made by the fallback
// export path survives a daemon that died mid-export. Incus deletes it at
// expiry; the normal path deletes it as soon as the stream is closed.
const exportBackupTTL = 6 * time.Hour

// wholeBoxBackupRequest is the export every whole-box backup takes:
//   - InstanceOnly: snapshot history is excluded (#2800 decision).
//   - OptimizedStorage false: a portable tarball of the filesystem, which
//     restores onto any pool driver, and which Incus's ZFS driver reads from
//     a temporary read-only snapshot, so a running box exports point-in-time.
//   - RootOnly false: attached custom volumes are included (#2800 decision).
//   - CompressionAlgorithm empty: the server default (gzip unless the host
//     sets backups.compression_algorithm).
func wholeBoxBackupRequest(name string) api.InstanceBackupsPost {
	return api.InstanceBackupsPost{
		Name:             name,
		InstanceOnly:     true,
		OptimizedStorage: false,
		RootOnly:         false,
	}
}

// pipeSink adapts an io.PipeWriter to the io.WriteSeeker the Incus client
// asks for. The client only writes (util.SafeCopy); a Seek would mean it
// changed that and is refused rather than silently corrupting the stream.
type pipeSink struct{ *io.PipeWriter }

func (pipeSink) Seek(int64, int) (int64, error) {
