package zap

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres coverage for the ZAP alert store (#1300).
//
// This store holds web-vulnerability findings and their lifecycle: open,
// suppressed, resolved. "Resolved" here does not mean a human checked
// anything — it means a later scan did not report the finding again, which the
// UI and any alerting on top read as "fixed".
//
// So the load-bearing property is *which* alerts a finishing scan is allowed
// to resolve. Resolving too few is noise; resolving too many silently closes
// live vulnerabilities and is the failure nobody sees, because a closed alert
// stops being displayed.
//
// The store had no test of any kind.

func zapTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dsn := os.Getenv("CONTAINARIUM_TEST_DSN")
	if dsn == "" {
		t.Skip("set CONTAINARIUM_TEST_DSN to run this against Postgres (the store-integration lane does)")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	store, err := NewStore(context.Background(), pool)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Fingerprints are globally UNIQUE, so every test needs its own namespace
	// or concurrent runs collide on the upsert.
	tag := fmt.Sprintf("t%d-%s", os.Getpid(), t.Name())
	t.Cleanup(func() {
		_, _ = store.pool.Exec(context.Background(),
			"DELETE FROM zap_alerts WHERE fingerprint LIKE $1", tag+"%")
	})
	return store, tag
}

func anAlert(fingerprint, risk, url string) Alert {
	return Alert{
		Fingerprint: fingerprint,
		PluginID:    "10038",
		AlertName:   "Content Security Policy Header Not Set",
		Risk:        risk,
		Confidence:  "High",
		Description: "no CSP header",
		URL:         url,
		Method:      "GET",
	}
}

// A finding seen again must keep its first-seen date and gain a new last-seen.
// "How long has this been open" is the question an operator asks of a
// vulnerability, and an upsert that rewrote first_seen_at would answer it with
// "since the last scan" forever.
func TestZapStore_ReSeenAlertKeepsItsFirstSeenDate(t *testing.T) {
	ctx := context.Background()
	store, tag := zapTestStore(t)

	run1, err := store.CreateScanRun(ctx, "manual", "alice-container")
	if err != nil {
		t.Fatalf("CreateScanRun: %v", err)
	}
	fp := tag + "-recurring"
	if err := store.SaveAlerts(ctx, run1, []Alert{anAlert(fp, "High", "https://a.example.com/")}); err != nil {
		t.Fatalf("SaveAlerts: %v", err)
	}

	var firstSeen, lastSeen string
	if err := store.pool.QueryRow(ctx,
		"SELECT first_seen_at::text, last_seen_at::text FROM zap_alerts WHERE fingerprint = $1", fp,
	).Scan(&firstSeen, &lastSeen); err != nil {
		t.Fatalf("read timestamps: %v", err)
	}

	// A second scan reports the same finding.
	run2, err := store.CreateScanRun(ctx, "scheduled", "alice-container")
	if err != nil {
		t.Fatalf("CreateScanRun(2): %v", err)
	}
	if err := store.SaveAlerts(ctx, run2, []Alert{anAlert(fp, "High", "https://a.example.com/")}); err != nil {
		t.Fatalf("SaveAlerts(2): %v", err)
	}

	var firstSeen2, lastSeen2 string
	if err := store.pool.QueryRow(ctx,
		"SELECT first_seen_at::text, last_seen_at::text FROM zap_alerts WHERE fingerprint = $1", fp,
	).Scan(&firstSeen2, &lastSeen2); err != nil {
		t.Fatalf("read timestamps after re-save: %v", err)
