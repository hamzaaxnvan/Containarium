package sentinel

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
)

// TestHMACSecretConfigured guards the #341 misconfig signal: the
// sentinel must report its keysync/certsync auth as misconfigured when
// CONTAINARIUM_SENTINEL_AUTH_SECRET is unset or shorter than the
// minimum, so /status can flag it for alerting.
func TestHMACSecretConfigured(t *testing.T) {
	tests := []struct {
		name   string
		secret []byte
		want   bool
	}{
		{"unset", nil, false},
		{"empty", []byte(""), false},
		{"too short", []byte("short"), false},
		{"one under min", []byte(strings.Repeat("x", auth.SentinelMinSecretLen-1)), false},
		{"exactly min", []byte(strings.Repeat("x", auth.SentinelMinSecretLen)), true},
		{"comfortably long", []byte(strings.Repeat("x", auth.SentinelMinSecretLen+16)), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{}
			m.SetHMACSecret(tt.secret)
			if got := m.HMACSecretConfigured(); got != tt.want {
				t.Errorf("HMACSecretConfigured() = %v, want %v (len=%d, min=%d)",
					got, tt.want, len(tt.secret), auth.SentinelMinSecretLen)
			}
		})
	}
}
