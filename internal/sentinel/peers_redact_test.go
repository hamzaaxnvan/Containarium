package sentinel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
)

// #3119: /sentinel/peers has no request auth, so pool names (and the ?pool=
// filter, which lets a caller test names) are redacted for unsigned callers.
// A request carrying a valid daemon signature keeps the full response.

func peersCall(t *testing.T, m *Manager, query string, signWith string) (string, []testPeer) {
	t.Helper()
	req := httptest.NewRequest("GET", "/sentinel/peers"+query, nil)
	if signWith != "" {
		auth.SignSentinelRequest(req, []byte(signWith))
	}
	rec := httptest.NewRecorder()
	m.PeersHandler()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var parsed struct {
		Peers []testPeer `json:"peers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	return rec.Body.String(), parsed.Peers
}
