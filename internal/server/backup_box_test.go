package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"testing"

	"filippo.io/age"

	"github.com/footprintai/containarium/pkg/core/backup"
)

// --- #2801 PR B: server fixtures for the whole-box create path ---

// serverFakeBox exports a minimal Incus-shaped archive.
type serverFakeBox struct {
	archive  []byte
	exported []string
}

func (b *serverFakeBox) ExportInstance(name string) (io.ReadCloser, error) {
	b.exported = append(b.exported, name)
	return io.NopCloser(bytes.NewReader(b.archive)), nil
}

func (b *serverFakeBox) BoxDiskUsage(string) (int64, error) { return 1 << 20, nil }

type serverFakeUploader struct{}

func (serverFakeUploader) Upload(string, string) error            { return nil }
func (serverFakeUploader) DownloadStream(string, io.Writer) error { return os.ErrNotExist }
func (serverFakeUploader) Delete(string) error                    { return nil }

func minimalBoxArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
