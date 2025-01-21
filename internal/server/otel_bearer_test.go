package server

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Phase 2.5 follow-up — OTel bearer load/create primitive.

func resetBearerCache(t *testing.T) {
	t.Helper()
	otelBearerOnce = sync.Once{}
	otelBearerValue = ""
	otelBearerError = nil
}

func clearOTelEnv(t *testing.T) {
	t.Helper()
	t.Setenv(otelBearerEnvOverride, "")
	t.Setenv(otelBearerEnvFile, "")
}

func TestLoadOrCreateOTelBearer_EnvOverride(t *testing.T) {
	resetBearerCache(t)
	clearOTelEnv(t)
	t.Setenv(otelBearerEnvOverride, "from-env")
