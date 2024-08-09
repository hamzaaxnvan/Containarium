package sentinel

import (
	"encoding/json"
	"net/http"
)

// CAHandler returns the peer-CA certificate PEM that daemons pin
// when calling other peers over HTTPS. Wrapped by the existing
// HMAC middleware (auth.SentinelHMACMiddleware) when registered —
// only callers that hold CONTAINARIUM_SENTINEL_AUTH_SECRET can
// fetch it.
//
// 503 when no peer-CA is configured. The daemon's bootstrap code
// uses that as a signal to fall back to plain HTTP (rollout mode);
// once the CA is on every sentinel the daemon should refuse to
// start.
func (m *Manager) CAHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		caPEM := m.CACertPEM()
		if caPEM == nil {
			http.Error(w, `{"error":"peer CA not configured on this sentinel","code":503}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
