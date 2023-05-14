package config

import "testing"

func clearAuthEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvStrictScopes, "")
}

// TestLoadAuthDefaults verifies the permissive default posture (#1679): strict
// mode is off unless explicitly armed.
