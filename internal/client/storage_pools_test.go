package client

import (
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// SystemInfo.storage_pools decodes into the core type `containarium info`
// renders (#2823). TestGetSystemInfoDecodesQuotedInt64 covers gateway JSON.
func TestStoragePoolsFromWire(t *testing.T) {
	want := []incus.StoragePoolCapacity{{
