//go:build integration

// Real-Postgres coverage for the entries a purge of the hash-chained audit_logs
// writes (#2979). The retention.sweep entry is written before the purge and can
// name only the chain base the purge starts from; the retention.sweep.result
// entry is written after and names the base it ended on. Both go through the
// real RetentionServer into a real audit store, and are read back from
// audit_logs, so what is checked is what an operator would find.
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/retention/pgstore/
package pgstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/retention"
	"github.com/footprintai/containarium/internal/server"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

const (
	sweepAction  = "retention.sweep"
	resultAction = "retention.sweep.result"
)

// sweepEntryDetail and resultEntryDetail read back the JSON the server writes.
type sweepEntryDetail struct {
	Stores []struct {
		Class          string `json:"class"`
		Store          string `json:"store"`
		PreviousBaseID int64  `json:"previous_base_id"`
	} `json:"stores"`
}

type resultEntryDetail struct {
	Store          string `json:"store"`
	PreviousBaseID int64  `json:"previous_base_id"`
	BaseID         int64  `json:"base_id"`
	RowsRemoved    int64  `json:"rows_removed"`
	Classes        []struct {
		Class   string `json:"class"`
		Removed int64  `json:"removed"`
	} `json:"classes"`
}

// refusing is an audit logger over the real store that refuses one action.
type refusing struct {
	*audit.Store
	action string
}

func (r refusing) Log(ctx context.Context, e *audit.AuditEntry) error {
	if e.Action == r.action {
