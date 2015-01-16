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
	return 0, errors.New("export stream is not seekable")
}

// ExportInstance streams the instance as an Incus backup tarball. It works on
// a running instance. The caller must Close the reader; closing early
// aborts the export.
//
// No temp file on the daemon's side: on a server with the direct_backup
// extension the tarball is streamed as Incus produces it. An older server
// (Incus 6.0 LTS) can only export a stored backup, so one is created in
// Incus's own backups directory, streamed, and deleted on Close.
func (c *Client) ExportInstance(name string) (io.ReadCloser, error) {
	if name == "" {
		return nil, errors.New("export: instance name is required")
	}
	if _, _, err := c.server.GetInstance(name); err != nil {
		return nil, fmt.Errorf("export %s: %w", name, err)
	}
	pr, pw := io.Pipe()
	sink := &incusclient.BackupFileRequest{BackupFile: pipeSink{pw}}

	if c.server.HasExtension("direct_backup") {
		go func() {
			err := c.server.CreateInstanceBackupStream(name, wholeBoxBackupRequest(""), sink)
			pw.CloseWithError(wrapErr("export "+name, err))
		}()
		return pr, nil
	}

	backupName := fmt.Sprintf("containarium-export-%d", time.Now().UnixNano())
	req := wholeBoxBackupRequest(backupName)
	req.ExpiresAt = time.Now().Add(exportBackupTTL)
	op, err := c.server.CreateInstanceBackup(name, req)
	if err == nil {
		err = op.Wait()
	}
	if err != nil {
		_ = pr.Close()
		return nil, fmt.Errorf("export %s: create backup: %w", name, err)
	}
	go func() {
		_, err := c.server.GetInstanceBackupFile(name, backupName, sink)
		pw.CloseWithError(wrapErr("export "+name, err))
	}()
	return &storedExport{PipeReader: pr, remove: func() error {
		op, err := c.server.DeleteInstanceBackup(name, backupName)
		if err == nil {
			err = op.Wait()
		}
		if err != nil {
			log.Printf("[incus] export %s: remove stored backup %s: %v (Incus expires it in %s)", name, backupName, err, exportBackupTTL)
		}
		return err
	}}, nil
}
