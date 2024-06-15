package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

// Tests for #2405: a hook may declare the format of what it emits. A hook
// that declares HookFormatPGCustom promises a `pg_dump -Fc` archive, so the
// daemon can restore-test it and restore it into a scratch target exactly
// like a database-mode dump. An undeclared (or OPAQUE) hook keeps every
// refusal it had before.

// sampleTOC is what `pg_restore --list` prints for a small custom-format
// archive: three user tables (one of them partitioned, plus one partition),
// their data entries, and objects that must NOT be counted as relations.
const sampleTOC = `;
; Archive created at 2026-10-09 02:00:01 UTC
;     dbname: app
;     TOC Entries: 12
;     Format: CUSTOM
;
; Selected TOC Entries:
;
215; 1259 16386 TABLE public accounts app
216; 1259 16390 TABLE public events app
217; 1259 16394 TABLE public events_2026 app
3329; 0 0 TABLE ATTACH public events_2026 app
218; 1259 16398 SEQUENCE public accounts_id_seq app
219; 1259 16400 VIEW public active_accounts app
220; 1259 16402 FOREIGN TABLE public remote_things app
3328; 0 16386 TABLE DATA public accounts app
3330; 0 16394 TABLE DATA public events_2026 app
3200; 2606 16405 CONSTRAINT public accounts accounts_pkey app
3201; 1259 16406 INDEX public events_idx app
`

// hookFormatOps layers `pg_restore --list` on top of the restore-test fake
// so Create's manifest step for a PG_CUSTOM hook has a TOC to read.
type hookFormatOps struct {
	*verifyOps
	tocListing string
	failList   bool
}

func newHookFormatOps(payload []byte) *hookFormatOps {
	o := &hookFormatOps{verifyOps: newVerifyOps(payload), tocListing: sampleTOC}
	// The scratch database restored from sampleTOC holds the same three
	// user relations the archive declares.
	o.restoredRelations = "3"
	return o
}

func (h *hookFormatOps) ExecWithOutput(container string, command []string) (string, string, error) {
	full := strings.Join(command, " ")
	if strings.Contains(full, "pg_restore --list") {
		h.record(container, command)
		if h.failList {
			return "", "bash: line 1: pg_restore: command not found", errExec
		}
		return h.tocListing, "", nil
	}
	return h.verifyOps.ExecWithOutput(container, command)
}

func (h *hookFormatOps) ran(container, needle string) bool {
	for _, c := range h.execByContainer[container] {
		if strings.Contains(c, needle) {
			return true
		}
	}
	return false
}

const testHook = "/opt/backup/db-dump.sh"

func createHookBackup(t *testing.T, m *Manager, format HookFormat, recipient string) *Record {
	t.Helper()
	rec, err := m.Create(CreateOptions{
		Username:      "alice",
		ContainerName: "alice-container",
		Destination:   DestLocal,
		Hook:          testHook,
		HookFormat:    format,
		AgeRecipient:  recipient,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return rec
}

func TestCreate_HookPGCustomRecordsRelationCount(t *testing.T) {
	ops := newHookFormatOps([]byte("PGDMP-custom-archive"))
	m := newTestManager(t, ops)
	rec := createHookBackup(t, m, HookFormatPGCustom, "")

	if rec.Engine != EngineHook {
		t.Fatalf("engine = %q, want %q", rec.Engine, EngineHook)
	}
	if rec.HookFormat != HookFormatPGCustom {
		t.Errorf("HookFormat = %q, want %q", rec.HookFormat, HookFormatPGCustom)
	}
	if rec.RelationCount == nil || *rec.RelationCount != 3 {
		t.Fatalf("RelationCount = %v, want 3 (tables in the archive's TOC)", rec.RelationCount)
	}

	// The TOC is read from the staged dump inside the source container,
	// before the staged copy is removed.
	cmds := ops.execByContainer["alice-container"]
	listAt, rmAt := -1, -1
	for i, c := range cmds {
		if strings.Contains(c, "pg_restore --list") && strings.Contains(c, "/tmp/containarium-backup-"+rec.ID+".dump") {
			listAt = i
		}
		if strings.HasPrefix(c, "rm -f /tmp/containarium-backup-"+rec.ID) {
			rmAt = i
		}
	}
	if listAt < 0 {
		t.Fatalf("pg_restore --list never ran on the staged dump; commands: %v", cmds)
	}
	if rmAt >= 0 && rmAt < listAt {
		t.Errorf("staged dump removed (cmd %d) before its TOC was read (cmd %d)", rmAt, listAt)
	}

	// Persisted, not just returned.
