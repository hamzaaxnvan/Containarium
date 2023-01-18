package sentinel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Spot-preemption observability (#514 follow-up).
//
// The monitoring stack (VictoriaMetrics + vmalert + Alertmanager) runs in a
// container ON the backend spot VM, so it dies WITH the spot — it cannot
// alert on the spot's own preemption. The always-on sentinel is the only
// component that survives the outage, so it owns this signal, exposed two
// ways:
//
//   - An outbound webhook fired the instant the spot is preempted ("down")
//     and the instant it returns to proxy ("recovered"/"up"). Self-contained;
//     depends on nothing on the spot.
//   - A Prometheus /metrics endpoint with two monotonic counters
//     (sentinel_preempted_total / sentinel_recovered_total) plus point-in-time
//     gauges, for an EXTERNAL scraper to alert on the net
//     (preempted_total - recovered_total > 0 == "currently down").
//
// Both run from the sentinel's single event-loop goroutine (fireAlert is
// called from handleEvent / healthCheckAll); the HTTP /metrics read takes a
// consistent snapshot via the exported getters.

// alertPayload is the JSON body POSTed to AlertWebhookURL.
type alertPayload struct {
	Event          string `json:"event"` // "preempted" | "recovered"
	Backend        string `json:"backend"`
	PreemptedTotal int    `json:"preempted_total"`
	RecoveredTotal int    `json:"recovered_total"`
	// Outstanding is preempted_total - recovered_total: > 0 means the spot
	// is currently down (an unrecovered preemption). This is the "net" to
	// alert on; included so a dumb webhook consumer can branch without state.
	Outstanding   int    `json:"outstanding"`
	OutageSeconds int64  `json:"outage_seconds,omitempty"` // set on "recovered"
	Timestamp     string `json:"timestamp"`
}

// fireAlert POSTs an alert to the configured webhook, best-effort and
// asynchronous so it never blocks (or fails) the event loop. A nil/empty
// AlertWebhookURL is a no-op. The counters are read on the calling
// goroutine (consistent snapshot) and captured into the payload before the
// POST is dispatched.
func (m *Manager) fireAlert(event, backend string, outage time.Duration) {
	if m.config.AlertWebhookURL == "" {
		return
	}
	payload := alertPayload{
		Event:          event,
		Backend:        backend,
		PreemptedTotal: m.preemptCount,
		RecoveredTotal: m.recoveredCount,
		Outstanding:    m.preemptCount - m.recoveredCount,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	}
	if outage > 0 {
		payload.OutageSeconds = int64(outage.Seconds())
	}
	postAlertPayload(m.config.AlertWebhookURL, payload)
}

// postAlertPayload delivers an alertPayload to url, best-effort and
// asynchronous so it never blocks (or fails) a caller's event loop.
// Shared by fireAlert (primary "gcp" backend) and fireWatchOnlyAlert
// (watch_recovery.go) — the only difference between them is which
// backend's counters went into the payload.
func postAlertPayload(url string, payload alertPayload) {
	go func() {
		body, err := json.Marshal(payload)
		if err != nil {
			log.Printf("[sentinel] alert webhook: marshal: %v", err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
