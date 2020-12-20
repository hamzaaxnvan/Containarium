package sshauthz

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/prototext"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// memKeys is an in-memory registry for the unit run of the decider table.
// The registration semantics themselves are tested against Postgres.
type memKeys struct {
	rows map[string]KeyRecord
	err  error
}

func newMemKeys(*testing.T) keySeeder { return &memKeys{rows: map[string]KeyRecord{}} }

func (m *memKeys) Lookup(_ context.Context, fingerprint string) (KeyRecord, error) {
	if m.err != nil {
		return KeyRecord{}, m.err
	}
	rec, ok := m.rows[fingerprint]
	if !ok {
		return KeyRecord{}, ErrKeyNotFound
	}
	return rec, nil
}

func (m *memKeys) InsertIfAbsent(_ context.Context, rec KeyRecord) (bool, error) {
	if _, ok := m.rows[rec.Fingerprint]; ok {
		return false, nil
	}
	m.rows[rec.Fingerprint] = rec
	return true, nil
}

func TestSelfHostDecider_Table(t *testing.T) {
	runDeciderCases(t, newMemKeys)
}

// A registry that cannot be read (or is not configured) fails closed:
// DENY with UPSTREAM_UNAVAILABLE (decided on #2517, #2550), never ALLOW
// and never a transport error. Nothing from the underlying failure
// (paths, DSNs, credentials) may reach the response.
func TestSelfHostDecider_UnreadableRegistryDenies(t *testing.T) {
	const leak = "/var/lib/secret/registry.db postgres://u:hunter2@db"
	for _, tc := range []struct {
