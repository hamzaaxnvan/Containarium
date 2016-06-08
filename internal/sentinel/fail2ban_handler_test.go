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
