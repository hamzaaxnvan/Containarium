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
