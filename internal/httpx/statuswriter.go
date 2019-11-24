// Package httpx holds small net/http helpers shared by the daemon's HTTP
// middleware so the next interface gap (Flusher, Hijacker, ReaderFrom, ...)
// is fixed in one place.
package httpx

import "net/http"

// StatusWriter wraps http.ResponseWriter to capture the status code the
// handler wrote. It is the status-recording writer the audit and metrics
// middleware share.
type StatusWriter struct {
	http.ResponseWriter
	StatusCode int
}

