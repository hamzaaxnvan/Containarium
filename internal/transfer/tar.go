package transfer

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// buildChangedTar writes a gzipped tar of the named files (paths
// forward-slash-relative to root) to w. Returns the number of bytes
// written.
//
// Mode bits and timestamps are preserved — important for executables.
// Directories aren't written explicitly; tar's extract creates them as
// needed when files in them arrive (tar honors leading-path mkdir
// implicitly).
func buildChangedTar(root string, paths []string, w io.Writer) (int64, error) {
	cw := &countingWriter{w: w}
	gz := gzip.NewWriter(cw)
	tw := tar.NewWriter(gz)

	for _, rel := range paths {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		if err != nil {
			return 0, fmt.Errorf("stat %s: %w", abs, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return 0, fmt.Errorf("tar header %s: %w", abs, err)
		}
