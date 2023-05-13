package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
)

// FetchPeerPKI took `ctx interface{}`, both callers passed nil, and the
// request was built with http.NewRequest — so the parameter was untyped,
// unused, and unusable all at once.
//
// The consequence was not hypothetical: a daemon shutting down during the
// bootstrap fetch waited for the client's 15s timeout, because nothing could
// cancel the request. This asserts the context is now honoured.
func TestFetchPeerPKI_HonoursContextCancellation(t *testing.T) {
	// A sentinel that never answers, so only cancellation ends the call.
