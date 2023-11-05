package audit

import (
	"strings"
	"testing"
	"time"
)

// Phase 4.5 — hash-chain verification logic. These tests cover
// the pure-Go side (computeRowHash + VerifyChain). The DB-bound
// integration is exercised by the integration suite (requires a
// live Postgres).

func entry(ts int64, user, action, detail string) AuditEntry {
	return AuditEntry{
		Timestamp:    time.Unix(0, ts),
		Username:     user,
		Action:       action,
		ResourceType: "api",
		ResourceID:   "GET /v1/x",
		Detail:       detail,
		SourceIP:     "10.0.0.1",
		StatusCode:   200,
	}
}

func mustHash(t *testing.T, e *AuditEntry, prevHash string, version int16) string {
	t.Helper()
	h, err := computeRowHash(e, prevHash, version)
	if err != nil {
		t.Fatalf("computeRowHash: %v", err)
	}
	return h
}

func TestComputeRowHash_Deterministic(t *testing.T) {
	e := entry(123, "alice", "api_get", "duration=5s")
	h1 := mustHash(t, &e, "", CurrentHashVersion)
	h2 := mustHash(t, &e, "", CurrentHashVersion)
	if h1 != h2 {
		t.Fatalf("same entry produced different hashes: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("SHA-256 hex should be 64 chars, got %d", len(h1))
	}
}

func TestComputeRowHash_DistinctFieldsProduceDistinctHashes(t *testing.T) {
	base := entry(123, "alice", "api_get", "duration=5s")
	hBase := mustHash(t, &base, "", CurrentHashVersion)

	// Each field change must perturb the hash.
	cases := []struct {
		name  string
		tweak func(e *AuditEntry)
	}{
		{"username", func(e *AuditEntry) { e.Username = "bob" }},
		{"action", func(e *AuditEntry) { e.Action = "api_post" }},
		{"detail", func(e *AuditEntry) { e.Detail = "duration=6s" }},
		{"status_code", func(e *AuditEntry) { e.StatusCode = 500 }},
		{"resource_id", func(e *AuditEntry) { e.ResourceID = "GET /v1/y" }},
		{"source_ip", func(e *AuditEntry) { e.SourceIP = "10.0.0.2" }},
		{"timestamp", func(e *AuditEntry) { e.Timestamp = time.Unix(0, 124) }},
		{"actor", func(e *AuditEntry) { e.Actor = "human-alice" }},
		{"delegation_chain", func(e *AuditEntry) { e.DelegationChain = `{"sub":"human-alice"}` }},
		{"token_id", func(e *AuditEntry) { e.TokenID = "jti-123" }},
		{"org_id", func(e *AuditEntry) { e.OrgID = "org-1" }},
		{"run_id", func(e *AuditEntry) { e.RunID = "run-1" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := base
			tc.tweak(&e)
			if mustHash(t, &e, "", CurrentHashVersion) == hBase {
				t.Fatalf("changing %s did not perturb the hash", tc.name)
