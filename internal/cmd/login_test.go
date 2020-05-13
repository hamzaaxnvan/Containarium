package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/credentials"
)

// withTempHome redirects $HOME so the credentials store writes
// under a tempdir for the test. Mirrors the helper in
// internal/credentials/store_test.go; duplicated here to keep this
// test file self-contained (the rule from Phase 1 is "don't modify
// shared test files"; "don't share helpers either" is the safe
// reading).
func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	// Reset the package-level flag globals between tests so a
	// previous test's --token / --server doesn't leak — on the way in as
	// well as on the way out (#2922).
	authToken, serverAddr = "", ""
	t.Cleanup(func() {
		authToken = ""
		serverAddr = ""
		loginServer = ""
		whoamiServerFlag = ""
		configTokenSrv = ""
		logoutAll = false
		loginNoBrowser = false
		loginDeviceName = ""
	})
	return home
}

// seedCreds writes a credentials file with the supplied servers.
func seedCreds(t *testing.T, home string, defServer string, srvs map[string]credentials.ServerCreds) string {
	t.Helper()
	cf := credentials.NewCredentialsFile()
	for k, v := range srvs {
		cf.Set(k, v)
	}
	if defServer != "" {
		cf.DefaultServer = defServer
	}
	path := filepath.Join(home, credentials.DefaultRelPath)
	if err := credentials.Save(path, cf); err != nil {
		t.Fatalf("seed credentials: %v", err)
	}
	return path
}

func TestResolveAuthToken_PrecedenceChain(t *testing.T) {
	home := withTempHome(t)
	_ = seedCreds(t, home, "https://cloud.containarium.dev", map[string]credentials.ServerCreds{
		"https://cloud.containarium.dev":  {Token: "file-default"},
		"https://self-hosted.example.com": {Token: "file-self"},
	})

	cases := []struct {
		name      string
		setAuth   string // simulate --token / env (already collapsed into authToken)
		setServer string
		want      string
	}{
		{
			name:    "flag wins",
			setAuth: "explicit-flag",
			want:    "explicit-flag",
		},
		{
			name: "no flag, no server -> default_server token",
			want: "file-default",
		},
		{
			name:      "no flag, server matches non-default",
			setServer: "https://self-hosted.example.com",
			want:      "file-self",
		},
		{
			name:      "no flag, server with trailing slash still matches",
			setServer: "https://self-hosted.example.com/",
			want:      "file-self",
		},
		{
			name:      "no flag, unknown server -> empty",
			setServer: "https://nope.example.com",
			want:      "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			authToken = tc.setAuth
			serverAddr = tc.setServer
			defer func() { authToken = ""; serverAddr = "" }()
			got := resolveAuthToken(tc.setServer)
			if got != tc.want {
				t.Fatalf("resolveAuthToken = %s, want %s", tokenFingerprint(got), tokenFingerprint(tc.want))
			}
		})
	}
}

func TestResolveAuthToken_NoCredentialsFile(t *testing.T) {
	withTempHome(t)
	authToken = ""
	if got := resolveAuthToken(""); got != "" {
		t.Fatalf("resolveAuthToken with no file = %q, want empty", got)
	}
}

// fakeDeviceFlowServer runs an httptest server that mimics the
// cloud's CLISessionService just enough for the login command to
// drive a full happy-path. It returns "pending" for the first N
// status polls and "approved" thereafter.
type fakeDeviceFlowServer struct {
	srv          *httptest.Server
	approveAfter int32 // number of poll requests before flipping to approved
	pollCount    int32
	gotDevice    atomic.Value // string
}

func newFakeDeviceFlow(t *testing.T, approveAfter int32) *fakeDeviceFlowServer {
	t.Helper()
	f := &fakeDeviceFlowServer{approveAfter: approveAfter}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/cli/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		var req createSessionReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.gotDevice.Store(req.DeviceName)
		_ = json.NewEncoder(w).Encode(createSessionResp{
			SessionID:       "sess-1",
			UserCode:        "WXYZ-1234",
			VerificationURL: f.srv.URL + "/cli/authorize?code=WXYZ-1234",
			ExpiresIn:       600,
		})
	})
	mux.HandleFunc("/v1/cli/sessions/sess-1/status", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&f.pollCount, 1)
		resp := sessionStatusResp{Status: "pending"}
		if n > f.approveAfter {
			resp = sessionStatusResp{
				Status:    "approved",
				Token:     "ctnr_test_token",
				UserEmail: "alice@example.com",
				OrgID:     "org_42",
				ExpiresAt: time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339),
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// TestCreateSession_DecodesCloudCamelCaseWire pins the POST /v1/cli/sessions
// WIRE contract with HAND-WRITTEN lowerCamelCase JSON (captured from the live
// cloud's grpc-gateway output) — NOT marshaled from createSessionResp. The
// fakeDeviceFlowServer round-trips the struct, so it can't catch a tag drift;
// this can. An earlier snake_case version of the struct decoded this body to
// all-zero and login failed with "incomplete session".
func TestCreateSession_DecodesCloudCamelCaseWire(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sessionId":"sess-1","userCode":"WXYZ-1234",` +
			`"verificationUrl":"https://cloud.example/cli/authorize?code=WXYZ-1234",` +
			`"expiresInSeconds":600,"pollingIntervalSeconds":5,"expiresAt":"2026-06-03T08:43:25Z"}`))
	}))
	defer srv.Close()

	got, err := createSession(context.Background(), srv.Client(), srv.URL, "cloud-mcp")
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}
	if got.SessionID != "sess-1" || got.UserCode != "WXYZ-1234" {
		t.Errorf("sessionId/userCode not decoded: %+v", got)
	}
	if got.VerificationURL == "" {
		t.Error("verificationUrl not decoded (would trip the incomplete-session guard)")
	}
	if got.ExpiresIn != 600 {
		t.Errorf("expiresInSeconds = %d, want 600", got.ExpiresIn)
	}
}

// TestFetchSessionStatus_NormalizesProtoEnum pins two wire facts: (1) the
// cloud emits CLISessionStatus as the proto-enum NAME
// ("CLI_SESSION_STATUS_APPROVED"), which the handler folds to the short token
// the poll loop switches on; (2) the status response carries NO email/org —
// the cloud's GetCLISessionStatusResponse has only status/token/expires_at/
// approved_at, so identity rides the token, not these fields.
func TestFetchSessionStatus_NormalizesProtoEnum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"CLI_SESSION_STATUS_APPROVED","token":"ctnr_test",` +
			`"expiresAt":"2026-06-03T08:53:30Z"}`))
	}))
	defer srv.Close()

	st, err := fetchSessionStatus(context.Background(), srv.Client(), srv.URL+"/status")
	if err != nil {
		t.Fatalf("fetchSessionStatus: %v", err)
	}
	if st.Status != "approved" {
		t.Errorf("Status = %q, want normalized %q", st.Status, "approved")
	}
	if st.Token != "ctnr_test" {
		t.Errorf("Token = %s, want %s", tokenFingerprint(st.Token), tokenFingerprint("ctnr_test"))
	}
	if st.UserEmail != "" || st.OrgID != "" {
		t.Errorf("email/org must be empty (cloud status response omits them): email=%q org=%q", st.UserEmail, st.OrgID)
	}
}

func TestLogin_HappyPath_WritesCredentials(t *testing.T) {
	home := withTempHome(t)
	f := newFakeDeviceFlow(t, 1) // pending once, then approved
	loginServer = f.srv.URL
	loginNoBrowser = true

	// Run with a short poll interval so the test is fast.
	old := loginPollIntervalForTest()
	defer restorePollInterval(old)
	setPollInterval(20 * time.Millisecond)

	var out bytes.Buffer
	loginCmd.SetOut(&out)
	loginCmd.SetErr(&out)
	if err := runLogin(loginCmd, nil); err != nil {
		t.Fatalf("runLogin: %v", err)
	}

	// Credentials file written with the token.
	path := filepath.Join(home, credentials.DefaultRelPath)
	cf, err := credentials.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	creds, ok := cf.Get(f.srv.URL)
	if !ok {
		t.Fatalf("credentials not stored for %s (file holds %d server(s))", f.srv.URL, len(cf.Servers))
