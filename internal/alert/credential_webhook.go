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
