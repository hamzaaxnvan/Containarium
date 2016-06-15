package sentinel

import (
	"crypto/hmac"
	"crypto/md5"  // #nosec G501 -- test only: proves no MD5 of the secret leaks
	"crypto/sha1" // #nosec G505 -- test only: proves no SHA-1 of the secret leaks
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
)

// #2892: /status reports, per primary, whether its last daemon-AUTH-gated
// request authenticated or was rejected with 401 — and nothing derived from
// the secret. These tests drive the REAL route table (buildBinaryServerMux),
// so a route that loses its observing gate fails them.

// The detail route is gated by the ADMIN secret (testAdminSecret), which
// daemons do not hold.
const wrongPrimariesSecret = "wrong-secret-but-still-32-bytes!!"

func newAuthHealthTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	m := &Manager{
		backends:    NewBackendPool(),
		primaries:   NewPrimaryRegistry(),
		certStore:   NewCertStore(),
		hmacSecret:  []byte(testPrimariesSecret),
		adminSecret: []byte(testAdminSecret),
	}
	m.state.Store(StateProxy)
	srv := httptest.NewServer(buildBinaryServerMux("/nonexistent/containarium", m))
	t.Cleanup(srv.Close)
	return srv
}

// statusView is /status plus, for the assertions that need to know WHICH
// primary is failing, the per-pool detail from the auth-gated route. The
// detail is fetched with a valid signature; /status itself never carries it.
type statusView struct {
	StatusJSON
	PrimaryAuthHealth []PrimaryAuthHealthJSON
}

func getStatus(t *testing.T, srv *httptest.Server) (statusView, string) {
	t.Helper()
	code, body := doPrimaries(t, srv, http.MethodGet, "/status", "", false, nil)
	if code != http.StatusOK {
		t.Fatalf("/status: code %d body %q", code, body)
	}
	var v statusView
	if err := json.Unmarshal([]byte(body), &v.StatusJSON); err != nil {
		t.Fatalf("/status: decode %q: %v", body, err)
	}
	v.PrimaryAuthHealth, _ = getAuthHealthDetail(t, srv)
	return v, body
}

func getAuthHealthDetail(t *testing.T, srv *httptest.Server) ([]PrimaryAuthHealthJSON, string) {
	t.Helper()
	code, body := doPrimaries(t, srv, http.MethodGet, primaryAuthHealthPath, testAdminSecret, true, nil)
	if code != http.StatusOK {
		t.Fatalf("%s: code %d body %q", primaryAuthHealthPath, code, body)
	}
	var rep PrimaryAuthHealthReportJSON
	if err := json.Unmarshal([]byte(body), &rep); err != nil {
		t.Fatalf("%s: decode %q: %v", primaryAuthHealthPath, body, err)
	}
	return rep.Primaries, body
}

func registerPool(t *testing.T, srv *httptest.Server, pool string) {
	t.Helper()
	body := fmt.Sprintf(`{"pool":%q,"hostname":"%s.example.com","port":443}`, pool, pool)
	if code, resp := doPrimaries(t, srv, http.MethodPost, "/sentinel/primaries", testPrimariesSecret, true, strings.NewReader(body)); code != http.StatusCreated {
		t.Fatalf("register %s: code %d body %q", pool, code, resp)
	}
}

func healthFor(st statusView, pool string) *PrimaryAuthHealthJSON {
	for i := range st.PrimaryAuthHealth {
		if st.PrimaryAuthHealth[i].Pool == pool {
			return &st.PrimaryAuthHealth[i]
		}
	}
	return nil
}

func TestStatus_PrimaryAuthHealth_ReportsSuccessThen401ThenRecovery(t *testing.T) {
	srv := newAuthHealthTestServer(t)
	registerPool(t, srv, "pool-a")

	st, _ := getStatus(t, srv)
	h := healthFor(st, "pool-a")
	if h == nil || h.State != PrimaryAuthOK || h.LastSuccess == "" || h.LastFailure != "" || h.FailuresSinceSuccess != 0 {
		t.Fatalf("after register: health = %+v, want ok with last_success only", h)
	}

	// The primary's heartbeat now signs with a secret the sentinel does not
	// hold (a partial rotation): 401, attributed to the primary by its source
	// address learned from the authenticated registration.
	if code, _ := doPrimaries(t, srv, http.MethodPut, "/sentinel/primaries/pool-a", wrongPrimariesSecret, true, nil); code != http.StatusUnauthorized {
		t.Fatalf("mismatched heartbeat: code %d, want 401", code)
	}
	// Other daemon-AUTH routes count too (peer-cert bootstrap, key resync).
	if code, _ := doPrimaries(t, srv, http.MethodPost, "/sentinel/keys/resync", wrongPrimariesSecret, true, nil); code != http.StatusUnauthorized {
		t.Fatalf("mismatched resync: code %d, want 401", code)
	}
	st, _ = getStatus(t, srv)
	h = healthFor(st, "pool-a")
	if h == nil || h.State != PrimaryAuthFailing401 || h.LastFailure == "" || h.FailuresSinceSuccess != 2 {
		t.Fatalf("after 401s: health = %+v, want failing_401 with 2 failures", h)
	}
	if st.UnattributedAuthFailures != 0 {
		t.Errorf("unattributed = %d, want 0 (both 401s came from the primary's address)", st.UnattributedAuthFailures)
	}

	if code, _ := doPrimaries(t, srv, http.MethodPut, "/sentinel/primaries/pool-a", testPrimariesSecret, true, nil); code != http.StatusOK {
		t.Fatalf("good heartbeat: code %d", code)
	}
	st, _ = getStatus(t, srv)
	if h = healthFor(st, "pool-a"); h == nil || h.State != PrimaryAuthOK || h.FailuresSinceSuccess != 0 {
		t.Fatalf("after recovery: health = %+v, want ok with 0 failures", h)
	}
}

// A deregistered primary keeps its health record: a daemon restarted with a
// new secret deregisters with the old one (accepted) and then 401s on
// re-registration — that 401 is exactly the signal the runbook needs.
func TestStatus_PrimaryAuthHealth_SurvivesDeregister(t *testing.T) {
	srv := newAuthHealthTestServer(t)
	registerPool(t, srv, "pool-a")
	if code, _ := doPrimaries(t, srv, http.MethodDelete, "/sentinel/primaries/pool-a", testPrimariesSecret, true, nil); code != http.StatusNoContent {
		t.Fatalf("deregister: code %d", code)
	}
	body := `{"pool":"pool-a","hostname":"pool-a.example.com","port":443}`
	if code, _ := doPrimaries(t, srv, http.MethodPost, "/sentinel/primaries", wrongPrimariesSecret, true, strings.NewReader(body)); code != http.StatusUnauthorized {
		t.Fatalf("re-register with new secret: code %d, want 401", code)
	}
	st, _ := getStatus(t, srv)
	if h := healthFor(st, "pool-a"); h == nil || h.State != PrimaryAuthFailing401 {
		t.Fatalf("health = %+v, want failing_401 after deregister + 401", h)
	}
}

// An unauthenticated caller cannot create per-primary state, whatever pool
// name it puts in the path or body: no primary has authenticated yet, so the
// 401s only bump the bounded unattributed counter.
func TestStatus_PrimaryAuthHealth_UnauthenticatedCreatesNoPrimaryState(t *testing.T) {
	srv := newAuthHealthTestServer(t)
	for i := 0; i < 50; i++ {
		pool := fmt.Sprintf("attacker-%d", i)
		doPrimaries(t, srv, http.MethodPut, "/sentinel/primaries/"+pool, wrongPrimariesSecret, true, nil)
		doPrimaries(t, srv, http.MethodPost, "/sentinel/primaries", "", false,
			strings.NewReader(fmt.Sprintf(`{"pool":%q,"hostname":"x.example.com","port":443}`, pool)))
	}
	st, _ := getStatus(t, srv)
	if len(st.PrimaryAuthHealth) != 0 || st.PrimaryAuthCounts != (PrimaryAuthCountsJSON{}) {
		t.Fatalf("primary_auth_health = %+v counts %+v, want empty: unauthenticated requests must not create entries", st.PrimaryAuthHealth, st.PrimaryAuthCounts)
	}
	if st.UnattributedAuthFailures != 100 || st.LastUnattributedAuthFailure == "" {
		t.Fatalf("unattributed = %d (last %q), want 100 with a timestamp", st.UnattributedAuthFailures, st.LastUnattributedAuthFailure)
	}
}

// The pool name in a rejected request is never trusted. With two primaries
// behind one source address (attribution is ambiguous), a bad request naming
// pool-a must not mark pool-a (or pool-b) failing.
func TestStatus_PrimaryAuthHealth_401NamingAPoolDoesNotPoisonIt(t *testing.T) {
	srv := newAuthHealthTestServer(t)
	registerPool(t, srv, "pool-a")
	registerPool(t, srv, "pool-b")
	if code, _ := doPrimaries(t, srv, http.MethodPut, "/sentinel/primaries/pool-a", wrongPrimariesSecret, true, nil); code != http.StatusUnauthorized {
		t.Fatalf("code %d, want 401", code)
	}
