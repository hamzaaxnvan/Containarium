package server

import (
	"testing"
	"time"

	appconfig "github.com/footprintai/containarium/internal/config"
)

// The safety property of the whole enforcement change: a daemon that configures
// nothing gets nothing. If this ever returns non-nil for an unconfigured
// operator, every existing deployment starts denying traffic on an upgrade.
func TestGatewayPolicyFromEnv_UnconfiguredIsNil(t *testing.T) {
	if got := gatewayPolicyFromEnv(); got != nil {
		t.Fatalf("unconfigured env produced a policy: %+v", got)
	}
}

// A window says when to measure, not how much is allowed — on its own it must
// not switch enforcement on.
func TestGatewayPolicyFromEnv_WindowAloneIsNotAPolicy(t *testing.T) {
	t.Setenv(appconfig.EnvGatewayQuotaWindow, "10m")
	if got := gatewayPolicyFromEnv(); got != nil {
		t.Fatalf("window alone produced a policy: %+v", got)
	}
}

func TestGatewayPolicyFromEnv_QuotaParsed(t *testing.T) {
	t.Setenv(appconfig.EnvGatewayQuotaWindow, "10m")
	t.Setenv(appconfig.EnvGatewayQuotaCalls, "500")
	t.Setenv(appconfig.EnvGatewayQuotaTokens, "1000000")
	t.Setenv(appconfig.EnvGatewayQuotaOutputTokens, "250000")

	got := gatewayPolicyFromEnv()
	if got == nil {
		t.Fatal("configured quota produced no policy")
	}
	if got.Quota.Window != 10*time.Minute {
		t.Errorf("window = %v, want 10m", got.Quota.Window)
	}
	if got.Quota.MaxCalls != 500 || got.Quota.MaxTotalTokens != 1_000_000 || got.Quota.MaxOutputTokens != 250_000 {
		t.Errorf("quota = %+v", got.Quota)
	}
	if got.Anomaly.Enabled {
		t.Error("anomaly detection enabled without being asked for")
	}
	if got.RevokeAt != 0 {
		t.Errorf("RevokeAt = %v, want 0 (auto-revocation off unless configured)", got.RevokeAt)
	}
}

func TestGatewayPolicyFromEnv_AnomalyAloneIsEnough(t *testing.T) {
	t.Setenv(appconfig.EnvGatewayAnomalyEnabled, "1")
	got := gatewayPolicyFromEnv()
	if got == nil {
		t.Fatal("anomaly=1 produced no policy")
	}
	if !got.Anomaly.Enabled {
		t.Error("anomaly not enabled")
	}
	if !got.Quota.IsZero() {
		t.Errorf("quota = %+v, want zero", got.Quota)
	}
}

// Garbage must fall back to "not configured" rather than to some accidental
// number: a typo'd cap that silently becomes 0 would read as unlimited, and one
// that silently becomes 1 would deny everything.
func TestGatewayPolicyFromEnv_MalformedValuesAreIgnored(t *testing.T) {
	t.Setenv(appconfig.EnvGatewayQuotaCalls, "many")
	t.Setenv(appconfig.EnvGatewayQuotaTokens, "-5")
	t.Setenv(appconfig.EnvGatewayQuotaWindow, "sometimes")
	t.Setenv(appconfig.EnvGatewayAnomalyRevokeAt, "yes")

	if got := gatewayPolicyFromEnv(); got != nil {
		t.Fatalf("malformed values produced a policy: %+v", got)
	}
}

func TestGatewayPolicyFromEnv_RevokeAtOptIn(t *testing.T) {
	t.Setenv(appconfig.EnvGatewayAnomalyEnabled, "1")
	t.Setenv(appconfig.EnvGatewayAnomalyRevokeAt, "0.95")

	got := gatewayPolicyFromEnv()
	if got == nil {
		t.Fatal("no policy")
	}
	if got.RevokeAt != 0.95 {
		t.Errorf("RevokeAt = %v, want 0.95", got.RevokeAt)
	}
}
