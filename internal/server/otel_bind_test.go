package server

import (
	"strings"
	"testing"
)

// Phase 2.5 — OTel collector bind address is configurable via env
// var (audit C-HIGH-5). Default stays 0.0.0.0 for backwards compat;
// paranoid operators can pin to a specific bridge IP.

func TestOTelReceiverBindAddress_DefaultIsZero(t *testing.T) {
	t.Setenv("CONTAINARIUM_OTEL_COLLECTOR_BIND", "")
	if got := otelReceiverBindAddress(); got != "0.0.0.0" {
		t.Fatalf("default bind = %q, want 0.0.0.0", got)
	}
}

func TestOTelReceiverBindAddress_OverrideHonored(t *testing.T) {
