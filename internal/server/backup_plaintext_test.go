package server

import (
	"testing"

	"github.com/footprintai/containarium/pkg/core/backup"
)

// #3169: only a record with no key at all counts as stored in plaintext.
func TestBackupStoredPlaintext(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  backup.Record
