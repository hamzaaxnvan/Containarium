package transfer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildChangedTar_PreservesModeAndContent(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello world"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bin", "run.sh"), []byte("#!/bin/sh\n"), 0o755))

	var buf bytes.Buffer
	n, err := buildChangedTar(root, []string{"hello.txt", "bin/run.sh"}, &buf)
	require.NoError(t, err)
	assert.Greater(t, n, int64(0))

	gz, err := gzip.NewReader(&buf)
	require.NoError(t, err)
	tr := tar.NewReader(gz)

	got := map[string]struct {
		mode    int64
		content []byte
	}{}
