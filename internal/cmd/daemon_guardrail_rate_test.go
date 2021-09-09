//go:build !windows && !containarium_client

package cmd

import (
	"strings"
	"testing"
)

// TestDaemonGuardrailRateFlags pins the documented defaults of the guardrail
// rate limits (#2404) and that each names its env override.
func TestDaemonGuardrailRateFlags(t *testing.T) {
	for _, tc := range []struct{ flag, def, env string }{
		{"guardrail-deploy-rate-per-minute", "6", "CONTAINARIUM_GUARDRAIL_DEPLOY_RATE_PER_MINUTE"},
