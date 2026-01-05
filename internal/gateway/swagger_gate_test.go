package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
)

// Phase 5.1 — /swagger-ui and /swagger.json must require
// the admin role. Exposing the full API surface to any
// authenticated caller (let alone unauthenticated) is the
// audit finding A-LOW-1; the gate closes it.
//
// We can't trivially exercise the full mux setup here, so
// the test targets the requireAdminFromContext helper in
// isolation. The auth-middleware step in front of it is
