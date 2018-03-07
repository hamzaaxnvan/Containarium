package sshsession

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestJSONLRecorder_StableFieldNames guards acceptance criterion 6:
// records are machine-readable with stable field names, no regex needed.
// A plain map[string]interface{} decode must find every documented field
// under its exact JSON name.
func TestJSONLRecorder_StableFieldNames(t *testing.T) {
	var buf bytes.Buffer
	rec := Record{
		SessionID:  "sess-1",
		Phase:      SessionPhaseOpen,
		OccurredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		ClientIP:   "203.0.113.7",
		ClientPort: 52341,
		Login:      "boxuser",
		Target:     "192.0.2.5:20022",
		AuthMethod: AuthMethodCertificate,
		Credential: Credential{
			KeyID:          "alice",
