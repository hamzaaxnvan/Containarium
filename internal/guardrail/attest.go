package guardrail

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"google.golang.org/protobuf/proto"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// SubjectDigest is the canonical SHA-256 of a directory: one line per
// regular file, "<slash-separated relative path>\n<sha256 of content>\n",
// in sorted path order, hashed together. Symlinks are not followed — they
// are skipped, like every non-regular file — so a tree cannot pull bytes
// from outside itself into its own digest. It depends on content and
// names only, never on mtimes or modes, so the same tree produced on two
// machines digests the same.
//
// Walks and opens go through os.Root (as internal/transfer does), so every
// open is kernel-enforced to stay inside dir even if the tree changes
// under us between the walk and the open. Files are opened with O_NONBLOCK
// and re-checked as regular on the open handle, so a path swapped for a
// FIFO between the walk and the open is skipped instead of hanging
// (#2480; same pattern as internal/guardrailstage).
func SubjectDigest(dir string) (string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()

	paths, err := regularFiles(root)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, rel := range paths {
		f, err := root.OpenFile(filepath.FromSlash(rel), os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return "", err
		}
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return "", err
		}
		if !info.Mode().IsRegular() {
			_ = f.Close()
			continue
		}
		fh := sha256.New()
		_, cerr := io.Copy(fh, f)
		_ = f.Close()
		if cerr != nil {
			return "", cerr
		}
		fmt.Fprintf(h, "%s\n%s\n", rel, hex.EncodeToString(fh.Sum(nil)))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// regularFiles lists the regular files under root as sorted slash paths.
func regularFiles(root *os.Root) ([]string, error) {
	var paths []string
	err := fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
