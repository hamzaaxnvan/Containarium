package audit

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/httpx"
)

// skipPaths are request paths that should not be audit-logged (noisy/non-sensitive)
var skipPaths = []string{
	"/health",
	"/v1/events/subscribe",
	"/swagger-ui/",
	"/webui/",
	"/grafana/",
	"/certs",
	"/authorized-keys",
	"/swagger.json",
}

// skipGETPaths are GET-only paths that generate too much noise from UI polling.
// These are read-only listing/status endpoints called every few seconds.
var skipGETPaths = []string{
	"/v1/containers",
	"/v1/apps",
	"/v1/metrics",
	"/v1/network/routes",
	"/v1/network/passthrough",
	"/v1/network/topology",
	"/v1/network/acl-presets",
	"/v1/network/dns-records",
	"/v1/system/info",
	"/v1/system/monitoring",
	"/v1/system/core-services",
	"/v1/security/clamav-summary",
	"/v1/security/clamav-reports",
	"/v1/security/scan-status",
	"/v1/audit/logs",
	"/v1/audit/health",
}

// HTTPAuditMiddleware wraps an HTTP handler to record audit log entries for
// API requests. store takes the auditLogger interface (narrowed to
// Log(ctx, *AuditEntry) error, same as GRPCInterceptor) rather than the
// concrete *Store, so tests can substitute a fake without a real Postgres
// connection — production callers pass *Store unchanged.
func HTTPAuditMiddleware(next http.Handler, store auditLogger) http.Handler {
	// Buffered channel for async writes
	entryCh := make(chan *AuditEntry, 256)

	// Background writer goroutine
	go func() {
		for entry := range entryCh {
			if err := store.Log(context.Background(), entry); err != nil {
				log.Printf("audit: failed to write log: %v", err)
				recordPersistFailure()
			}
		}
	}()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip noisy paths
		for _, prefix := range skipPaths {
			if strings.HasPrefix(r.URL.Path, prefix) {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Skip read-only GET polling endpoints (UI refreshes every few seconds)
		if r.Method == http.MethodGet {
			for _, prefix := range skipGETPaths {
				if strings.HasPrefix(r.URL.Path, prefix) {
					next.ServeHTTP(w, r)
