package threatdetect

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/alert"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// webhookURLConfigKey and webhookSecretConfigKey are the exact
// DaemonConfigStore keys internal/server/alert_server.go already persists
// the operator's webhook URL/secret under — reused so operators configure
// alerting once, not once per delivery path.
const (
	webhookURLConfigKey    = "alert_webhook_url"
	webhookSecretConfigKey = "alert_webhook_secret" // #nosec G101 -- a DaemonConfigStore key NAME, not a credential value
)

const (
	notifierQueueCap    = 256
	notifierMaxAttempts = 3
	notifierHTTPTimeout = 10 * time.Second
)

// WebhookConfigSource is the subset of app.DaemonConfigStore the notifier
// needs. A structural interface (rather than depending on package app
// directly) keeps this package's dependency graph one-directional and the
// notifier table-testable with a fake.
type WebhookConfigSource interface {
	Get(ctx context.Context, key string) (string, error)
}

// Notifier is what FindingStore/MemFindingStore call after successfully
// persisting a finding. nil is valid on either store: no notifier means no
// webhook delivery (e.g. in tests that don't exercise it).
type Notifier interface {
	Notify(f *Finding)
}

// WebhookNotifier delivers MEDIUM+ findings straight to the operator's
// configured webhook — deliberately bypassing vmalert/alertmanager (design
// doc: that path needs the VictoriaMetrics container and is metrics-shaped;
// a direct POST works on any backend, including minimal BYOC hosts). It
// reuses the same webhook URL/secret config and HMAC-SHA256 signing scheme
// the existing alert-webhook relay uses, so operators configure alerting
// once.
//
// Delivery is asynchronous relative to the store write that triggers it:
// Notify enqueues onto a bounded channel and returns immediately. One
// worker goroutine drains it and does the actual HTTP POST with retry and
// backoff. A full queue or a dead webhook drops/fails deliveries (recorded
// as failed, when a DeliveryStore is wired) but never blocks the caller —
// the caller is the detection hot path, which must never stall behind a
// slow or unreachable webhook.
type WebhookNotifier struct {
	config   WebhookConfigSource
	delivery *alert.DeliveryStore // nil = attempts are logged, not persisted
	client   *http.Client
	queue    chan *Finding
	gate     *deliveryGate
}

// NewWebhookNotifier builds a notifier. config is required — without
// somewhere to read the webhook URL/secret from, the notifier can't do
// anything. delivery is optional (nil in degraded mode, when there's no
// Postgres pool to back a DeliveryStore).
func NewWebhookNotifier(config WebhookConfigSource, delivery *alert.DeliveryStore) *WebhookNotifier {
	return &WebhookNotifier{
		config:   config,
		delivery: delivery,
		client:   &http.Client{Timeout: notifierHTTPTimeout},
		queue:    make(chan *Finding, notifierQueueCap),
		gate:     newDeliveryGate(DefaultRenotifyInterval),
	}
}

// SetRenotifyInterval changes how long an open finding stays quiet after a
// delivery before the same condition is delivered again (#2643); <= 0
// restores DefaultRenotifyInterval. Call before the first Notify.
func (n *WebhookNotifier) SetRenotifyInterval(d time.Duration) {
	n.gate = newDeliveryGate(d)
}

// Start runs the delivery worker until ctx is done. Call once, before the
// first Notify.
func (n *WebhookNotifier) Start(ctx context.Context) {
	go func() {
