package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeEmail(t *testing.T) {
	ok := map[string]string{" Alice@Example.com ": "alice@example.com", "bob+box@example.org": "bob+box@example.org"}
	for in, want := range ok {
		if got, err := normalizeEmail(in); err != nil || got != want {
			t.Errorf("%q → %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "not-an-email", "Alice <alice@example.com>", "a@b", "two@x.com, three@y.com"} {
		if got, err := normalizeEmail(in); err == nil {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
}

func TestRemindMeCmd_WriteAndClear(t *testing.T) {
	old := remindMeFile
	remindMeFile = filepath.Join(t.TempDir(), "remind-me")
	t.Cleanup(func() { remindMeFile = old; remindMeClear = false })
