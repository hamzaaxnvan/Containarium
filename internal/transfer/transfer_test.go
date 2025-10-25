package transfer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolve_UnresolvedSentinelIsTyped locks in that a missing sentinel
// (no field, no $CONTAINARIUM_SENTINEL_HOST) surfaces as ErrSentinelUnresolved
// so callers can errors.Is it and attach surface-specific guidance, and that
// the message points at the container's ssh_host rather than only the env var.
func TestResolve_UnresolvedSentinelIsTyped(t *testing.T) {
	t.Setenv("CONTAINARIUM_SENTINEL_HOST", "")
	opt := &Options{
		Username:  "alice",
		KeyPath:   writeKey(t),
		LocalPath: t.TempDir(),
	}
	err := opt.resolve()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSentinelUnresolved), "must be matchable with errors.Is")
	assert.Contains(t, err.Error(), "ssh_host", "message should point the caller at ssh_host")
}

// keyFile is a minimal test helper: writes a file at path so the
// readability check in Options.resolve() passes.
func writeKey(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "key-*")
	require.NoError(t, err)
	_, _ = f.WriteString("not a real ssh key — just satisfying the readability check\n")
	require.NoError(t, f.Close())
	return f.Name()
}
