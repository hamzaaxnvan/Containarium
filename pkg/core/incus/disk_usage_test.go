package incus

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBuildContainerRootfsPath nails down the path-resolution
// convention for the dir-backend disk-usage fallback. If incus
// ever moves where it puts pool sources we want this test to fail
// before the fallback silently starts looking at the wrong tree.
func TestBuildContainerRootfsPath(t *testing.T) {
	tests := []struct {
		name          string
		pool          string
		poolSource    string
		containerName string
		want          string
	}{
		{
			name:          "explicit absolute source (zfs pool source path)",
			pool:          "default",
			poolSource:    "/srv/containarium/storage",
			containerName: "alice",
			want:          "/srv/containarium/storage/containers/alice/rootfs",
		},
		{
