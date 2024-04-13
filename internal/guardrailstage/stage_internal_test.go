package guardrailstage

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func withHook(t *testing.T, h func(point, name string)) {
	t.Helper()
	testHook = h
	t.Cleanup(func() { testHook = nil })
}

func newArea(t *testing.T) (*Area, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "staging")
	put(t, filepath.Join(root, "tenant", "ds", "mine.txt"), "mine\n")
	put(t, filepath.Join(root, "tenant", "ds", "sub", "deep.txt"), "deep\n")
	put(t, filepath.Join(root, "other", "ds", "theirs.txt"), "another tenant's data\n")
	area, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	return area, root
}

// TestValidRef pins the lexical layer on its own: every row is refused by
// validRef itself, before any filesystem lookup could catch it.
func TestValidRef(t *testing.T) {
	for _, ref := range []string{"", ".", "..", "../x", "/etc", "/", `tenant\ds`, `a\..\b`, "a/./b", "a//b", "a/", "a/b/..", "./a"} {
		if err := validRef(ref); !errors.Is(err, ErrBadRef) {
			t.Errorf("validRef(%q) = %v, want ErrBadRef", ref, err)
		}
