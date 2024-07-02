package notify

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveNtfy publishes one push to a real ntfy server. Opt-in, skipped in
// CI: CONTAINARIUM_NTFY_E2E=<server>/<topic> (e.g. https://ntfy.example.com/my-test),
// with CONTAINARIUM_NTFY_E2E_TOKEN for a topic that needs one.
func TestLiveNtfy(t *testing.T) {
	target := os.Getenv("CONTAINARIUM_NTFY_E2E")
