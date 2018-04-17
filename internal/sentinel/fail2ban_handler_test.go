package sentinel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
)

const fail2banAdminSecret = "abcdefghijklmnopqrstuvwxyz0123456789ABCD" // 41 bytes

// fakeFail2BanRunner is a scripted Fail2BanRunner (#1962) — lets the
// handler tests below exercise every branch of the fail2ban-client
// output parsing and error classification without a real fail2ban
// install, which most dev sandboxes and CI runners don't have.
// Responses are keyed by the joined args of the exact call expected;
// an unscripted call fails loudly instead of silently returning "".
type fakeFail2BanRunner struct {
	mu        sync.Mutex
	responses map[string]fakeFail2BanResponse
	calls     []string
}

type fakeFail2BanResponse struct {
	output string
	err    error
}

func newFakeFail2BanRunner() *fakeFail2BanRunner {
	return &fakeFail2BanRunner{responses: map[string]fakeFail2BanResponse{}}
}

func (f *fakeFail2BanRunner) on(resp fakeFail2BanResponse, args ...string) {
	f.responses[strings.Join(args, " ")] = resp
}

func (f *fakeFail2BanRunner) Run(args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.Join(args, " ")
	f.calls = append(f.calls, key)
	resp, ok := f.responses[key]
	if !ok {
		return "", fmt.Errorf("fakeFail2BanRunner: unscripted call: fail2ban-client %s", key)
	}
	return resp.output, resp.err
}

func (f *fakeFail2BanRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func newManagerForFail2BanTest(t *testing.T, runner Fail2BanRunner) *Manager {
	t.Helper()
	m := &Manager{backends: NewBackendPool()}
	m.SetAdminSecret([]byte(fail2banAdminSecret))
	m.SetFail2BanRunner(runner)
	return m
}

func signedGet(path, secret string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	auth.SignSentinelRequest(req, []byte(secret))
	return req
}

func signedPost(t *testing.T, path, secret string, body any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	auth.SignSentinelRequest(req, []byte(secret))
	return req
}

// ---- GET /sentinel/fail2ban/bans ----------------------------------

// TestFail2BanBansHandler_MultipleJailsSomeBannedSomeNot is the bans-
// listing happy path (#1962 acceptance criterion 1): jails are
// discovered dynamically (never hardcoded), and each jail's response
// carries BOTH its banned list and its live ignoreip exemptions.
func TestFail2BanBansHandler_MultipleJailsSomeBannedSomeNot(t *testing.T) {
	runner := newFakeFail2BanRunner()
	runner.on(fakeFail2BanResponse{output: "Status\n|- Number of jail:\t2\n`- Jail list:\tsshd, sshpiperd\n"}, "status")
	runner.on(fakeFail2BanResponse{output: "Status for the jail: sshd\n`- Actions\n   `- Banned IP list:\t203.0.113.7\n"}, "status", "sshd")
	runner.on(fakeFail2BanResponse{output: "These IP addresses/networks are ignored:\n|- 127.0.0.0/8\n`- 10.0.0.0/8\n"}, "get", "sshd", "ignoreip")
	runner.on(fakeFail2BanResponse{output: "Status for the jail: sshpiperd\n`- Actions\n   `- Banned IP list:\t\n"}, "status", "sshpiperd")
	runner.on(fakeFail2BanResponse{output: "No IP address/network is ignored\n"}, "get", "sshpiperd", "ignoreip")

	m := newManagerForFail2BanTest(t, runner)
	handler := auth.SentinelHMACMiddleware(m.adminSecret, m.Fail2BanBansHandler())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, signedGet("/sentinel/fail2ban/bans", fail2banAdminSecret))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got Fail2BanBansResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	want := Fail2BanBansResponse{Jails: []Fail2BanJailBans{
		{Jail: "sshd", Banned: []string{"203.0.113.7"}, IgnoreIP: []string{"127.0.0.0/8", "10.0.0.0/8"}},
		{Jail: "sshpiperd", Banned: []string{}, IgnoreIP: []string{}},
	}}
	if len(got.Jails) != len(want.Jails) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want.Jails {
		g, w := got.Jails[i], want.Jails[i]
		if g.Jail != w.Jail || !stringSlicesEqual(g.Banned, w.Banned) || !stringSlicesEqual(g.IgnoreIP, w.IgnoreIP) {
			t.Fatalf("jail %d: got %+v, want %+v", i, g, w)
		}
	}
}

// TestFail2BanBansHandler_404WhenFail2BanNotInstalled is acceptance
// criterion 4: fail2ban not being available must surface as 404, not
// 500, so a caller can tell "this sentinel cannot do that" apart from
// an actual server bug.
func TestFail2BanBansHandler_404WhenFail2BanNotInstalled(t *testing.T) {
	runner := newFakeFail2BanRunner()
	runner.on(fakeFail2BanResponse{err: fmt.Errorf(`exec: "fail2ban-client": executable file not found in $PATH`)}, "status")

	m := newManagerForFail2BanTest(t, runner)
	handler := auth.SentinelHMACMiddleware(m.adminSecret, m.Fail2BanBansHandler())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, signedGet("/sentinel/fail2ban/bans", fail2banAdminSecret))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d, body = %s", rec.Code, rec.Body.String())
	}
}

// TestFail2BanBansHandler_RejectsUnsignedRequest mirrors the existing
// tunnel-tokens / byoc-routes admin-gating tests: an unauthenticated
