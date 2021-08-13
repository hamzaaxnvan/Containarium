package sentinel

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
)

// Regression coverage for Phase 0.6 (finding C-CRIT-2): the
// sentinel's /sentinel/peers response must be HMAC-signed so a
// compromised network path can't inject attacker peer URLs that the
// daemon would then proxy container traffic through.

const peersTestSecret = "abcdefghijklmnopqrstuvwxyz0123456789ABCD" // 40 bytes

func newManagerForPeersTest(secret string) *Manager {
	m := &Manager{backends: NewBackendPool()}
	if secret != "" {
		m.SetHMACSecret([]byte(secret))
	}
	m.backends.Add(&Backend{ID: "tunnel-a", Type: BackendTunnel, Healthy: true, Pool: "prod"})
	m.backends.Add(&Backend{ID: "tunnel-b", Type: BackendTunnel, Healthy: false, Pool: "prod"})
	return m
}
