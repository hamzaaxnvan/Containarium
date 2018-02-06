package alert

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
)

// WebhookURLConfigKey and WebhookSecretConfigKey are the DaemonConfigStore
// keys internal/server/alert_server.go persists the operator's webhook
// URL/secret under (and internal/threatdetect/notifier.go reads) — reused so
// an operator configures alerting once for every delivery path.
const (
	WebhookURLConfigKey    = "alert_webhook_url"
	WebhookSecretConfigKey = "alert_webhook_secret" // #nosec G101 -- a config key NAME, not a credential value
)

const (
	credentialWebhookMaxAttempts = 3
	credentialWebhookHTTPTimeout = 10 * time.Second
)

// WebhookConfigSource is the subset of app.DaemonConfigStore the notifier
// reads (structural, so this package does not import package app).
type WebhookConfigSource interface {
	Get(ctx context.Context, key string) (string, error)
}

// CredentialWebhookNotifier POSTs a CredentialExpiredAlert to the operator's
// configured webhook, signed with the same "sha256=<hex>" HMAC header the
// alert relay and the threat-detection notifier send, retried a bounded
// number of times, and recorded in webhook_deliveries when a DeliveryStore
// is wired. Delivery is synchronous: its only caller is the watcher's own
// background sweep.
type CredentialWebhookNotifier struct {
	config   WebhookConfigSource
	delivery *DeliveryStore // nil = attempts are logged, not persisted
	client   *http.Client
	backoff  func(attempt int) time.Duration
}

// NewCredentialWebhookNotifier builds the notifier. delivery may be nil.
func NewCredentialWebhookNotifier(config WebhookConfigSource, delivery *DeliveryStore) *CredentialWebhookNotifier {
	return &CredentialWebhookNotifier{
		config:   config,
		delivery: delivery,
		client:   &http.Client{Timeout: credentialWebhookHTTPTimeout},
		backoff:  func(attempt int) time.Duration { return time.Duration(attempt) * 500 * time.Millisecond },
	}
}

// NotifyCredentialExpired delivers a. No webhook configured is a no-op.
func (n *CredentialWebhookNotifier) NotifyCredentialExpired(ctx context.Context, a CredentialExpiredAlert) {
	url, err := n.config.Get(ctx, WebhookURLConfigKey)
	if err != nil || url == "" {
		log.Printf("[credential-watch] %s for %s: no operator webhook configured, not delivered", CredentialExpiredAlertName, a.Box)
		return
	}
	secret, _ := n.config.Get(ctx, WebhookSecretConfigKey) // optional: unsigned if absent

	body, merr := json.Marshal(a)
	if merr != nil {
		log.Printf("[credential-watch] marshal alert for %s: %v", a.Box, merr)
		return
	}

	var lastErr error
	var lastStatus int
	start := time.Now()
	for attempt := 0; attempt < credentialWebhookMaxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(n.backoff(attempt)):
			}
		}
		lastStatus, lastErr = n.postOnce(ctx, url, secret, body)
		if lastErr == nil {
			break
		}
	}

	errMsg := ""
	if lastErr != nil {
		errMsg = lastErr.Error()
		log.Printf("[credential-watch] webhook delivery failed for %s: %v", a.Box, lastErr)
	}
	if n.delivery == nil {
		return
	}
	if rerr := n.delivery.Record(ctx, &WebhookDelivery{
		AlertName:    CredentialExpiredAlertName,
		Source:       credentialWatchDeliverySource,
		WebhookURL:   maskWebhookURL(url),
		Success:      lastErr == nil,
		HTTPStatus:   lastStatus,
		ErrorMessage: errMsg,
		PayloadSize:  len(body),
		DurationMs:   int(time.Since(start).Milliseconds()),
	}); rerr != nil {
		log.Printf("[credential-watch] record webhook delivery for %s: %v", a.Box, rerr)
	}
}
