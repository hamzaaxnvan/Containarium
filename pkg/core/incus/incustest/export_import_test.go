package incustest_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
)

// TestExportImportRoundTripMock pins the mock's default export/import
// behaviour (#2801): what an instance exports is what an import under a new
// name carries, an import never lands over an existing instance, and a
// missing instance exports as not-found. The whole-box backup create and
// restore paths are unit-tested against this, so it has to behave like the
// real seam where those paths branch.
func TestExportImportRoundTripMock(t *testing.T) {
	m := incustest.NewMockBackend()
	if err := m.CreateContainer(incus.ContainerConfig{Name: "src"}); err != nil {
