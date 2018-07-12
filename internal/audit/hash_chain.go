package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Phase 4.5 — audit-log tamper-evidence via hash chain.
//
// Each audit_logs row gets two new columns:
//   - row_hash:  SHA-256 of (this row's fields || prev_hash).
//   - prev_hash: the previous row's row_hash (empty for the
//                first row in the chain).
//
// A row tampered with after insert won't match its own row_hash,
// and every row after it has the wrong prev_hash — so a single
// edit anywhere in the table is detectable by walking the chain.
//
// The chain doesn't prove the log is COMPLETE (an attacker could
// delete the suffix of rows and leave the head intact), but it
// proves nothing has been MODIFIED or INSERTED — which is the
// threat audit C-MED-5+ flags. It also can't, by itself, catch a
// privileged attacker who rewrites a row AND correctly recomputes
// every hash after it — that produces a chain that IS internally
// consistent. anchor.go closes that gap (#1706): the chain's tip is
// periodically published to an external sink Postgres can't rewrite,
// and VerifyChainAgainstAnchor checks against it.

const (
