package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/credentials"
	"github.com/footprintai/containarium/internal/sshkey"
)

// Tests for sub-task A7 (umbrella-issue #100): the
// `containarium login --with-ssh-setup / --no-ssh-setup` integration
// landed in login.go's runLogin tail-call to maybeRunPostLoginSSHSetup.
//
// These tests intentionally live in a NEW file (per the brief's
// "lesson from Phase 1: don't modify shared test files"). They share
// the cmd-package globals with ssh_test.go and login_test.go but
// redeclare nothing — withSSHHome (defined in ssh_test.go) is reused
// because its cleanup already resets the ssh-setup-related flag
// globals.

// withSSHLoginHome stacks ssh_test.go's withSSHHome with extra
// cleanup for the A7-only flag globals (loginWithSSHSetup,
// loginNoSSHSetup, loginPromptReader).
func withSSHLoginHome(t *testing.T) string {
	t.Helper()
	home := withSSHHome(t)
	t.Cleanup(func() {
		loginWithSSHSetup = false
		loginNoSSHSetup = false
		loginPromptReader = nil
		loginServer = ""
		loginNoBrowser = false
		loginDeviceName = ""
	})
	return home
}

// fakeLoginAndSSHKeysServer wires the device-flow endpoints from
// login.go AND the SSH-keys endpoints from ssh.go on a single
// httptest.Server, because the post-login auto-setup path expects to
// hit the same host the login ran against.
type fakeLoginAndSSHKeysServer struct {
	srv           *httptest.Server
	addedKeys     []addSSHKeyReq
	keys          []sshkey.SSHKey
	approveAfter  int32
	pollCount     int32
	sawAddSSHKey  atomic.Bool
	receivedToken string
}

func newFakeLoginAndSSHKeysServer(t *testing.T, approveAfter int32) *fakeLoginAndSSHKeysServer {
	t.Helper()
	mux := http.NewServeMux()
	f := &fakeLoginAndSSHKeysServer{approveAfter: approveAfter}

	mux.HandleFunc("/v1/cli/sessions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(createSessionResp{
			SessionID:       "sess-A7",
			UserCode:        "WXYZ-7777",
			VerificationURL: "http://example.invalid/cli/authorize",
			ExpiresIn:       600,
		})
	})
	mux.HandleFunc("/v1/cli/sessions/sess-A7/status", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&f.pollCount, 1)
		resp := sessionStatusResp{Status: "pending"}
		if n > f.approveAfter {
			resp = sessionStatusResp{
				Status:    "approved",
				Token:     "tok-A7",
				UserEmail: "alice@example.com",
				OrgID:     "org_42",
				ExpiresAt: time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339),
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	// SSH-key Add endpoint. Records the bearer token so the test can
	// assert the login → ssh-setup handoff used the freshly-issued
	// credential.
	mux.HandleFunc("/v1/user/ssh-keys", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(listSSHKeysResp{Keys: f.keys})
			return
		}
		f.receivedToken = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		f.sawAddSSHKey.Store(true)
		var req addSSHKeyReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.addedKeys = append(f.addedKeys, req)
		fp, _ := sshkey.Fingerprint(req.PublicKey)
		_ = json.NewEncoder(w).Encode(addSSHKeyResp{
			Key: sshkey.SSHKey{
				Name:        req.Name,
				PublicKey:   req.PublicKey,
				Fingerprint: fp,
				CreatedAt:   time.Now().UTC(),
			},
		})
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// runLoginWithSSHFlagsHelper performs the common test-setup boilerplate
