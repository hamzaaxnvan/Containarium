package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/auth"
)

// registerCookieSession wires POST/DELETE /v1/auth/session.
//
// Why this exists: the webui authenticates API calls with a JWT in
// localStorage, sent as `Authorization: Bearer <jwt>`. That works for
// fetch() / XHR. It does NOT work for browser-issued requests like
// <iframe src=...>, top-level navigations, or <img>/<link> — the
// browser cannot attach a header from JS to those, so any same-origin
// embed (notably the /grafana/ reverse proxy on the monitoring page)
// hits the auth middleware bare and returns 401. See issue #338.
//
// POST /v1/auth/session promotes the bearer token into a cookie that
// the browser DOES attach to those requests. The auth middleware reads
// either, with the header winning when both are present. The cookie's
// value is the raw JWT, so revocation, expiry, and the Phase 1.6
// refresh-token rejection all work identically — no new credential
// material is minted by this endpoint.
//
// DELETE /v1/auth/session clears the cookie. Called on logout / when
// the webui drops the server entry.
func registerCookieSession(mux *http.ServeMux, authMW *auth.AuthMiddleware) {
	mux.HandleFunc("/v1/auth/session", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			handleSessionCookieSet(w, r, authMW)
