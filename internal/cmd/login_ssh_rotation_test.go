package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/credentials"
	"github.com/footprintai/containarium/internal/sshkey"
)

func TestLogin_SSHKeyExpiryNudge(t *testing.T) {
	for _, tc := range []struct {
		name string
		days int
		warn bool
	}{
		{"seven days", 7, true},
		{"eight days", 8, false},
		{"already expired", -1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withSSHLoginHome(t)
			loginNoSSHSetup, loginNoBrowser = true, true
			expires := time.Now().UTC().Add(time.Duration(tc.days) * 24 * time.Hour).Format(time.RFC3339)
			var listed atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/cli/sessions":
					_ = json.NewEncoder(w).Encode(createSessionResp{SessionID: "expiry-test", UserCode: "TEST-1234", VerificationURL: "https://example.invalid/approve", ExpiresIn: 600})
				case "/v1/cli/sessions/expiry-test/status":
					_ = json.NewEncoder(w).Encode(sessionStatusResp{Status: "approved", Token: "expiry-token"})
				case "/v1/user/ssh-keys":
					if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer expiry-token" {
						t.Errorf("expected authenticated GET after login: %s, auth=%q", r.Method, r.Header.Get("Authorization"))
					}
					listed.Store(true)
					fmt.Fprintf(w, `{"keys":[{"name":"laptop","expiresAt":%q}]}`, expires)
				default:
					http.NotFound(w, r)
