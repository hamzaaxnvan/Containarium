package backup

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"filippo.io/age"
)

// writeFileForTest overwrites a staged dump so a test can simulate
// on-disk corruption behind the record's back.
func writeFileForTest(path string, content []byte) error {
	return os.WriteFile(path, content, 0o600)
}

// verifyOps extends the dump/restore fake with the extra behaviour a
// restore test drives: CREATE/DROP DATABASE against the maintenance
// database, pg_restore into the scratch database, and the post-restore
// sanity query. Every command is recorded per-container so a test can
// assert that the *source* container was never touched.
type verifyOps struct {
	*fakeOps

	// execByContainer records every command executed, keyed by the
	// container it ran against. The whole point of AC #1 is that the
	// source container's key stays absent.
	execByContainer map[string][]string

	// failRestore makes pg_restore report an engine error, simulating a
	// dump that hashes fine but cannot be loaded.
	failRestore bool
	// failCreateDB makes CREATE DATABASE fail.
	failCreateDB bool

	// sourceRelations is the manifest count Create records from the
	// source database; restoredRelations is what the scratch database
	// reports after the restore. They are separate so a test can
	// simulate a dump that loads but arrives short — the truncated
	// export the checksum cannot see.
	sourceRelations   string
	restoredRelations string
}

func newVerifyOps(payload []byte) *verifyOps {
	return &verifyOps{
		fakeOps:           newFakeOps(payload),
		execByContainer:   map[string][]string{},
		sourceRelations:   "42",
		restoredRelations: "42",
	}
}

func (v *verifyOps) record(container string, command []string) string {
	full := strings.Join(command, " ")
	v.execByContainer[container] = append(v.execByContainer[container], full)
	return full
}

func (v *verifyOps) Exec(container string, command []string) error {
	v.record(container, command)
	return v.fakeOps.Exec(container, command)
}

func (v *verifyOps) ExecWithOutput(container string, command []string) (string, string, error) {
	full := v.record(container, command)

	switch {
	case strings.Contains(full, "CREATE DATABASE"):
		if v.failCreateDB {
			return "", `ERROR:  permission denied to create database`, errExec
		}
		return "", "", nil
	case strings.Contains(full, "DROP DATABASE"):
		return "", "", nil
	case strings.Contains(full, "pg_restore"):
		if v.failRestore {
			return "", "pg_restore: error: could not read from input file: end of file", errExec
		}
		return "", "", nil
	case strings.Contains(full, "pg_class"):
		// The same query runs twice: once against the source at dump
		// time (the manifest) and once against the scratch database
		// after the restore. Tell them apart by the database queried.
		if strings.Contains(full, scratchPrefix) {
			return v.restoredRelations, "", nil
		}
		return v.sourceRelations, "", nil
	}
	return v.fakeOps.ExecWithOutput(container, command)
}

// seedBackup creates one LOCAL backup from alice's container and returns
// the manager plus the committed record.
func seedBackup(t *testing.T, ops ContainerOps) (*Manager, *Record) {
	t.Helper()
	m := newTestManager(t, ops)
	rec, err := m.Create(CreateOptions{
		Username:      "alice",
		ContainerName: "alice-container",
		Conn:          PgConn{Database: "app"},
		Destination:   DestLocal,
	})
	if err != nil {
		t.Fatalf("seed Create: %v", err)
	}
	return m, rec
}

// AC #1: verification runs against a disposable target and never touches
// the source container.
func TestVerifyNeverTouchesSourceContainer(t *testing.T) {
	ops := newVerifyOps([]byte("PGDMP-fake-archive-bytes"))
	m, rec := seedBackup(t, ops)

	// Forget the commands the seeding backup itself ran against the
	// source; we care only about what Verify does from here.
	ops.execByContainer = map[string][]string{}

	v, err := m.Verify(VerifyOptions{
		ID:              rec.ID,
		TargetContainer: "scratch-container",
		SourceContainer: "alice-container",
