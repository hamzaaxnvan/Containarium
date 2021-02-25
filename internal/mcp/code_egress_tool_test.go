package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// code_egress_policy reads over the same REST path `containarium code
// egress-policy get --http` uses, and prints through the same function.
func TestHandleCodeEgressPolicy(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"effective":{"tenant":"alice","source":"CODING_TOOL_EGRESS_POLICY_SOURCE_TENANT",` +
			`"restricted":true,"mode":"NETWORK_POLICY_MODE_ENFORCE","denyAll":true,"revision":"4",` +
			`"implicit":[{"kind":"CODING_TOOL_EGRESS_IMPLICIT_KIND_DNS_RESOLVER","description":"resolver"}]}}`))
	}))
	defer server.Close()
