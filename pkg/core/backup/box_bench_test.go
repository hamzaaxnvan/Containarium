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
