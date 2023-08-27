package config

import "testing"

// TestLoadSentinelEmpty verifies all fields are empty when nothing is set.
func TestLoadSentinelEmpty(t *testing.T) {
	for _, k := range []string{
		EnvSentinelAddr, EnvSentinelAlertWebhook, EnvSentinelAuthSecret,
		EnvSentinelCertSANs, EnvSentinelHost, EnvSentinelHTTPSPort,
		EnvSentinelPublicKey, EnvSentinelSigningKey, EnvSentinelURL,
	} {
		t.Setenv(k, "")
	}
	if got := LoadSentinel(); got != (Sentinel{}) {
		t.Errorf("LoadSentinel with empty env = %+v, want zero value", got)
	}
}

// TestLoadSentinelReadsEnv verifies Load maps each variable to its field.
func TestLoadSentinelReadsEnv(t *testing.T) {
	t.Setenv(EnvSentinelHost, "sentinel.example.com")
	t.Setenv(EnvSentinelAuthSecret, "supersecretvalue")
	t.Setenv(EnvSentinelHTTPSPort, "8443")

	s := LoadSentinel()
	if s.Host != "sentinel.example.com" {
		t.Errorf("Host = %q, want sentinel.example.com", s.Host)
	}
	if s.AuthSecret != "supersecretvalue" {
