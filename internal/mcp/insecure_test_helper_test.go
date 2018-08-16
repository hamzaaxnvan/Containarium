package mcp

import "os"

// All tests in this package use `httptest.NewServer`, which only
// speaks http://. The Phase 2.2 hardening refuses http:// baseURLs
