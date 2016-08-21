package transfer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWalkLocal_HashesAndModesContent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "b.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755))

	m, err := walkLocal(dir, nil)
	require.NoError(t, err)
	require.Len(t, m.entries, 2)

	a := m.entries["a.txt"]
	assert.Equal(t, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", a.Hash)
	assert.Equal(t, uint32(0o644), a.Mode)

	b := m.entries["sub/b.sh"]
	assert.Equal(t, uint32(0o755), b.Mode, "executable bit preserved")
}

func TestWalkLocal_ExcludeBySubstring(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "node_modules", "deep"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "node_modules", "deep", "x.js"), []byte("dont care"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep"), 0o644))

	m, err := walkLocal(dir, []string{"node_modules/"})
	require.NoError(t, err)
	require.Len(t, m.entries, 1)
