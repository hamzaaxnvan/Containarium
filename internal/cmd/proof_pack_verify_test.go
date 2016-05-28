//go:build !windows && !containarium_client

package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/proofpack"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// memSealChain is an in-memory chain whose rows are always intact; the real
// chain is covered by internal/proofpack's integration tests.
type memSealChain struct{ rows []audit.ChainEntry }

func (m *memSealChain) Append(_ context.Context, e *audit.AuditEntry) (int64, error) {
	row := audit.ChainEntry{AuditEntry: *e}
	row.ID = int64(len(m.rows) + 1)
	m.rows = append(m.rows, row)
	return row.ID, nil
}
func (m *memSealChain) ChainEntryAt(_ context.Context, id int64) (audit.ChainEntry, bool, error) {
	if id < 1 || id > int64(len(m.rows)) {
		return audit.ChainEntry{}, false, nil
	}
	return m.rows[id-1], true, nil
}
func (m *memSealChain) ChainBase(context.Context) (audit.ChainBase, bool, error) {
	return audit.ChainBase{}, false, nil
}
func (m *memSealChain) MaxRowID(context.Context) (int64, error) { return int64(len(m.rows)), nil }
func (m *memSealChain) VerifyChainSinceID(context.Context, int64, int) (int64, error) {
	return 0, nil
}

