package sentinel

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
)

// TestHMACSecretConfigured guards the #341 misconfig signal: the
// sentinel must report its keysync/certsync auth as misconfigured when
// CONTAINARIUM_SENTINEL_AUTH_SECRET is unset or shorter than the
