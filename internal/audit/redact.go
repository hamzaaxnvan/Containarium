package audit

import (
	"regexp"
	"strings"
)

// Phase 4.4 — audit-log redaction policy (audit C-MED-5).
//
// The audit_logs.detail column is a free-text TEXT field. Today
// the HTTP audit middleware writes only `duration=...` there, but
// nothing prevents a future handler from logging a request body
// containing a secret, a token, a password, etc. — and once a
// secret lands in audit_logs it's hard to scrub (the table is
// often replicated off-host for compliance retention).
//
// The defense is layered:
//   1. NEVER write request bodies or response bodies to audit
//      detail. The middleware enforces this — see middleware.go.
//   2. NEVER write query strings; the middleware records only
//      r.URL.Path. (?token= and similar are already deprecated
//      by Phase 1.5, but the middleware is the belt-and-braces
//      check.)
//   3. Whatever else does end up in `detail`, run it through
//      Redact before storage so common sensitive patterns are
//      scrubbed.
//
// The redaction is deliberately conservative — false-positives
// (over-redaction) are fine in an audit trail; false-negatives
// (a leaked secret) aren't.

// sensitivePatterns each have a regex that matches a known
// sensitive shape and a replacement string. The replacement keeps
// enough of the shape to remain useful for incident investigation
// while removing the secret material.
var sensitivePatterns = []struct {
	pattern *regexp.Regexp
	replace string
