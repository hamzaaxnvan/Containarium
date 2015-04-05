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
