package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/credentials"
	"github.com/footprintai/containarium/internal/sshkey"
)

// withSSHHome mirrors withTempHome from login_test.go but also
// resets the ssh-command-scope flag globals between runs so
// previous tests' --name / --key don't leak.
func withSSHHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	resetFlags := func() {
		authToken = ""
		serverAddr = ""
		sshSetupName = ""
		sshSetupKeyPath = ""
		sshSetupGenerate = false
		sshSetupForce = false
		sshSetupServer = ""
		sshListServer = ""
		sshRemoveServer = ""
		sshPropagateServer = ""
		sshPropagateBoxes = nil
		sshRotateName = ""
		sshRotateServer = ""
	}
	resetFlags()
	t.Cleanup(resetFlags)
	return home
}

func seedTokenFor(t *testing.T, home, server, token string) {
	t.Helper()
	cf := credentials.NewCredentialsFile()
	cf.Set(server, credentials.ServerCreds{
		Token:    token,
		IssuedAt: time.Now().UTC(),
	})
	path := filepath.Join(home, credentials.DefaultRelPath)
	if err := credentials.Save(path, cf); err != nil {
		t.Fatalf("seedTokenFor: %v", err)
	}
}

// fakeSSHKeysServer impersonates the cloud's UserService SSH-keys
// REST surface. We register only the routes the test needs and let
// the rest 404 (which the client turns into Unimplemented).
type fakeSSHKeysServer struct {
	srv     *httptest.Server
	mux     *http.ServeMux
	keys    []sshkey.SSHKey
	added   []addSSHKeyReq
	gotAuth string
}

func newFakeSSHKeysServer(t *testing.T) *fakeSSHKeysServer {
	t.Helper()
	f := &fakeSSHKeysServer{mux: http.NewServeMux()}
	f.srv = httptest.NewServer(f.mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSSHKeysServer) wireAdd() {
	f.mux.HandleFunc("/v1/user/ssh-keys", func(w http.ResponseWriter, r *http.Request) {
		f.gotAuth = r.Header.Get("Authorization")
		switch r.Method {
		case http.MethodPost:
			var req addSSHKeyReq
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.added = append(f.added, req)
			fp, _ := sshkey.Fingerprint(req.PublicKey)
			k := sshkey.SSHKey{
				Name:        req.Name,
				PublicKey:   req.PublicKey,
				Fingerprint: fp,
				CreatedAt:   time.Now().UTC(),
			}
			f.keys = append(f.keys, k)
			_ = json.NewEncoder(w).Encode(addSSHKeyResp{Key: k})
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(listSSHKeysResp{Keys: f.keys})
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	})
}

func (f *fakeSSHKeysServer) wireRemove() {
	// Subtree handler — strip the prefix in the handler.
	f.mux.HandleFunc("/v1/user/ssh-keys/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/v1/user/ssh-keys/")
		out := f.keys[:0]
		for _, k := range f.keys {
			if k.Name != name {
				out = append(out, k)
			}
		}
		f.keys = out
		w.WriteHeader(http.StatusOK)
	})
}

func TestSSHHTTPClient_ListSSHKeys_GatewayJSON(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
	}{
		{"camelCase", `{"keys":[{"name":"laptop","publicKey":"ssh-ed25519 example","fingerprint":"SHA256:example","createdAt":"2026-10-01T00:00:00Z","expiresAt":"2026-11-01T00:00:00Z","retiredAt":"2026-10-20T00:00:00Z"},{"name":"active","retiredAt":null}]}`},
		{"snake_case", `{"keys":[{"name":"laptop","public_key":"ssh-ed25519 example","fingerprint":"SHA256:example","created_at":"2026-10-01T00:00:00Z","expires_at":"2026-11-01T00:00:00Z","retired_at":"2026-10-20T00:00:00Z"},{"name":"active","retired_at":null}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/user/ssh-keys" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				_, _ = w.Write([]byte(tc.payload))
			}))
			defer srv.Close()
			client := &sshHTTPClient{server: srv.URL, token: "test-token", hc: srv.Client()}
			keys, err := client.ListSSHKeys(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(keys) != 2 {
				t.Fatalf("got %d keys, want 2", len(keys))
			}
