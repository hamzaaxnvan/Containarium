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
		t.Fatalf("create: %v", err)
	}
	archive := []byte("synthetic instance archive\x00\x01\x02")
	m.Archives = map[string][]byte{"src": archive}

	rc, err := m.ExportInstance("src")
	if err != nil {
		t.Fatalf("ExportInstance(src): %v", err)
	}
	exported, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("close export: %v", err)
	}
	if !bytes.Equal(exported, archive) {
		t.Fatalf("export = %q, want %q", exported, archive)
	}

	if err := m.DeleteContainer("src"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := m.ExportInstance("src"); !incus.IsNotFound(err) {
		t.Fatalf("export of a deleted instance: err = %v, want not-found", err)
	}

	if err := m.ImportInstance("restored", "default", bytes.NewReader(exported), incus.ImportOptions{}); err != nil {
		t.Fatalf("ImportInstance(restored): %v", err)
	}
	got, err := m.GetContainer("restored")
	if err != nil || got == nil {
		t.Fatalf("imported instance not present: %v, %v", got, err)
	}
	if got.State != "Stopped" {
		t.Errorf("imported instance state = %q, want Stopped (an import never starts the box)", got.State)
	}

	rc, err = m.ExportInstance("restored")
	if err != nil {
		t.Fatalf("re-export of the imported instance: %v", err)
	}
	again, _ := io.ReadAll(rc)
	_ = rc.Close()
