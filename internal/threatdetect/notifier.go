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
