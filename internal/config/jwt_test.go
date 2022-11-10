package config

import "testing"

func TestLoadJWTEmpty(t *testing.T) {
	for _, k := range []string{EnvJWTSecret, EnvJWTToken, EnvJWTTokenFile, EnvJWTAudience} {
		t.Setenv(k, "")
	}
	if got := LoadJWT(); got != (JWT{}) {
		t.Errorf("LoadJWT with empty env = %+v, want zero value", got)
	}
}

func TestLoadJWTReadsEnv(t *testing.T) {
