package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/auth"
)

// registerAuditEndpoint registers the audit logs query endpoint on the HTTP mux.
//
// Audit A-MED-3: tokens used to live in `?token=` here. Query
// strings get logged verbatim by every reverse proxy, load
// balancer, and browser history mechanism in the request path,
// which means an audit-trail dump of /v1/audit/logs?token=... was
// silently re-leaking the admin token to every hop. Authorization
// header only, like the rest of the API.
func registerAuditEndpoint(mux *http.ServeMux, store *audit.Store, authMW *auth.AuthMiddleware) {
	mux.HandleFunc("/v1/audit/logs", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authorizeAuditRead(w, r, authMW); !ok {
			return
		}
		handleAuditQuery(w, r, store)
	})

	// /v1/audit/health reports whether the async audit writers (HTTP
	// middleware, gRPC interceptor, event subscriber) have ever failed to
	// durably write an entry — a Log() error, or a full buffered channel
	// dropping one before Log() was even attempted. Both were previously
	// visible only as a log.Printf line in the daemon's stdout; a row that
	// was never written is otherwise undetectable, since nothing else
	// notices its absence. Same auth gate as /v1/audit/logs — this is
	// audit-trail integrity information, not a general health probe.
	mux.HandleFunc("/v1/audit/health", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authorizeAuditRead(w, r, authMW); !ok {
			return
		}
		report := audit.GetPersistFailureReport()
		resp := auditHealthResponse{PersistFailureCount: report.Count}
		if !report.Since.IsZero() {
			resp.PersistFailureSince = report.Since.UTC().Format(time.RFC3339)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
}

// authorizeAuditRead applies the same Bearer-token + (admin role OR
// audit:read scope) gate both /v1/audit/logs and /v1/audit/health use.
// Writes the error response and returns ok=false on any failure.
func authorizeAuditRead(w http.ResponseWriter, r *http.Request, authMW *auth.AuthMiddleware) (*auth.Claims, bool) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, `{"error": "unauthorized: Bearer token required in Authorization header", "code": 401}`, http.StatusUnauthorized)
		return nil, false
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	claims, err := authMW.ValidateToken(token)
	if err != nil {
		http.Error(w, `{"error": "unauthorized: invalid token", "code": 401}`, http.StatusUnauthorized)
		return nil, false
	}
	// #621: the audit log is sensitive (who-did-what across tenants). Gate
	// reads on admin role OR an explicit audit:read scope. NOTE: this is a
	// tightening — the endpoint previously accepted any valid token. A
	// non-admin consumer must now carry audit:read.
	if !auth.HasRole(claims.Roles, auth.RoleAdmin) && !auth.HasExplicitScope(claims.Scopes, auth.ScopeAuditRead) {
		http.Error(w, `{"error": "forbidden: requires admin role or audit:read scope", "code": 403}`, http.StatusForbidden)
		return nil, false
	}
	return claims, true
}

// auditHealthResponse is the JSON response for GET /v1/audit/health.
type auditHealthResponse struct {
	// PersistFailureCount is how many audit entries this process has
	// failed to durably write since startup.
	PersistFailureCount int64 `json:"persistFailureCount"`
	// PersistFailureSince is when the first failure was recorded (RFC3339),
	// omitted while PersistFailureCount is 0.
	PersistFailureSince string `json:"persistFailureSince,omitempty"`
}

// auditLogJSON is the JSON representation of a single audit log entry.
type auditLogJSON struct {
	ID           int64  `json:"id"`
	Timestamp    string `json:"timestamp"`
	Username     string `json:"username"`
	Action       string `json:"action"`
	ResourceType string `json:"resourceType"`
	ResourceID   string `json:"resourceId"`
	Detail       string `json:"detail"`
	SourceIP     string `json:"sourceIp"`
	StatusCode   int    `json:"statusCode"`
}

// auditLogsResponse is the JSON response for the audit logs endpoint.
type auditLogsResponse struct {
	Logs       []auditLogJSON `json:"logs"`
	TotalCount int32          `json:"totalCount"`
}

func handleAuditQuery(w http.ResponseWriter, r *http.Request, store *audit.Store) {
	q := r.URL.Query()

	params := audit.QueryParams{
		Username:     q.Get("username"),
		Action:       q.Get("action"),
		ResourceType: q.Get("resource_type"),
	}

	if fromStr := q.Get("from"); fromStr != "" {
		t, err := time.Parse(time.RFC3339, fromStr)
