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
			Serial:         42,
			CAFingerprint:  "SHA256:ca",
			KeyFingerprint: "SHA256:key",
		},
	}

	w := NewJSONLRecorder(&buf)
	if err := w.Record(rec); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &decoded); err != nil {
		t.Fatalf("decode JSONL line: %v", err)
	}

	for _, field := range []string{
		"session_id", "phase", "occurred_at", "client_ip", "client_port",
		"login", "target", "auth_method", "key_id", "serial",
		"ca_key_fingerprint", "key_fingerprint",
	} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("missing stable field %q in %v", field, decoded)
		}
	}

	if decoded["session_id"] != "sess-1" {
		t.Errorf("session_id = %v, want sess-1", decoded["session_id"])
	}
