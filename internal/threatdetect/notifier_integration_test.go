//go:build integration

// Integration coverage for WebhookNotifier's alert.DeliveryStore recording
// (#1643): the design doc's Notifier test-strategy row explicitly calls for
// "httptest server asserting payload + DeliveryStore row recorded" — the
// notifier_test.go unit tests all pass a nil DeliveryStore (delivery
// recording is optional there), so this is the one place that actually
// proves a delivery attempt lands in the real store an operator queries.
//
//	CONTAINARIUM_TEST_DSN=postgres://... go test -tags=integration ./internal/threatdetect/
package threatdetect

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/footprintai/containarium/internal/alert"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func deliveryPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("CONTAINARIUM_TEST_DSN")
	if dsn == "" {
		t.Fatal("CONTAINARIUM_TEST_DSN is unset. Failing rather than skipping, so a lane that " +
			"loses its database reports it instead of going quietly green.")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	// Each package run owns its schema. Initialize the delivery table here
	// rather than relying on internal/alert's tests to have created it.
	if _, err := alert.NewDeliveryStore(context.Background(), pool); err != nil {
		t.Fatalf("initialize webhook_deliveries: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM webhook_deliveries WHERE source = 'threatdetect'`); err != nil {
		t.Fatalf("reset (webhook_deliveries): %v", err)
	}
	return pool
}

// The #1643 acceptance test: a successful delivery is recorded in the real
// DeliveryStore an operator queries via the existing alert-delivery surface
// — not just logged.
