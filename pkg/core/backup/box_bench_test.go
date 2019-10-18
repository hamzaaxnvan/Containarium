package backup

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"runtime"
	"testing"
	"time"
)

// manyExcludedArchive streams a box with n small files under /var/cache and
// n/10 kept files. Nothing is buffered here, so the filter's own cost is
// what the benchmark sees.
func manyExcludedArchive(n int) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		gz, _ := gzip.NewWriterLevel(pw, gzip.BestSpeed)
		tw := tar.NewWriter(gz)
		body := make([]byte, 2048)
		write := func(name string) bool {
			if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
				pw.CloseWithError(err)
				return false
			}
			if _, err := tw.Write(body); err != nil {
				pw.CloseWithError(err)
				return false
			}
			return true
		}
		for i := 0; i < n; i++ {
			if !write(fmt.Sprintf("backup/container/rootfs/var/cache/apt/d%d/f%d", i%1000, i)) {
				return
