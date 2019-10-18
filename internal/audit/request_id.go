package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// Phase 4.6 — per-request correlation IDs (audit doc).
//
// Every audited request gets an X-Request-ID that's:
//   - Echoed in the response header so clients (and external
//     log aggregators) can correlate their view with the
//     daemon's audit row.
//   - Available on the request context for any handler that
//     wants to include it in error responses, peer-forwarded
//     RPC headers, or its own log lines.
//   - Stored in the audit row's Detail field (request_id=<id>)
//     so a single grep across audit_logs surfaces every
//     request from a given correlation ID.
//
