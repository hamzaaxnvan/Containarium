package server

import (
	"path/filepath"
	"testing"

	"github.com/footprintai/containarium/pkg/core/zfskey"
)

// Key custody wiring (#1342) — the switch that makes --encrypted reachable.
//
// This is the last piece of #1199 and the one that changes what an operator
// can do, so the tests here are about the switch being off by default and
// about it not depending on the order two startup calls happen to run in.

// An unset --zfs-keys-dir must leave custody unconfigured, which is what
// keeps validateEncryption refusing every encrypted create. Returning an
// error instead would stop the daemon from starting; returning a provider
// would silently turn the feature on for every existing deployment.
func TestKeyProviderFromDir_UnsetIsNotAnError(t *testing.T) {
	p, err := keyProviderFromDir("")
	if err != nil {
		t.Fatalf("an unset keys dir failed the daemon: %v", err)
	}
	if p != nil {
		t.Error("an unset keys dir produced a KeyProvider — encrypted creates would start " +
			"succeeding on every daemon that never asked for encryption")
	}
}

func TestKeyProviderFromDir_ConfiguredDirYieldsAProvider(t *testing.T) {
	p, err := keyProviderFromDir(filepath.Join(t.TempDir(), "keys"))
	if err != nil {
		t.Fatalf("keyProviderFromDir: %v", err)
	}
	if p == nil {
		t.Fatal("a configured keys dir produced no provider")
	}
}

// The ordering trap.
//
// SetEncryptionStorage builds the hooks holding whatever provider the server
