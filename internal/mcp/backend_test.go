package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newBackend classifies the target from the credential: a `ctnr_` hosted-
// control-plane API token → the cloud backend; a JWT (or nothing) → the OSS
// daemon backend.
func TestBackend_TargetClassification(t *testing.T) {
	if _, ok := newBackend(&Config{ServerURL: "https://cloud.example", JWTToken: "ctnr_abc.def"}).(cloudClient); !ok {
		t.Fatal("ctnr_ token must classify as the cloud backend")
	}
	if _, ok := newBackend(&Config{ServerURL: "https://daemon.example", JWTToken: "eyJhbGciOi.jwt"}).(*Client); !ok {
		t.Fatal("a JWT must classify as the OSS daemon backend")
	}
	if _, ok := newBackend(&Config{ServerURL: "https://daemon.example"}).(*Client); !ok {
		t.Fatal("no credential must default to the OSS daemon backend")
	}
}

// The cloud backend answers host-level operations with a clear "not available"
// CLIENT-SIDE — no round-trip the cloud could only reject. The server handler
// flips `hit`; it must stay false for every suppressed op.
func TestCloudBackend_HostOnlyOpsSuppressedWithoutNetwork(t *testing.T) {
	t.Setenv("CONTAINARIUM_MCP_ALLOW_INSECURE", "true")
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{}")
	}))
	defer srv.Close()

	cb := cloudClient{NewClient(srv.URL, "ctnr_test.tok")}
	args := map[string]interface{}{"username": "box1", "backend_id": "", "upgrade_id": "u1"}

	for _, tc := range []struct {
		name    string
		handler ToolHandler
	}{
		{"get_system_info", handleGetSystemInfo},
		{"check_for_updates", handleCheckForUpdates},
		{"debug_container", handleDebugContainer},
		{"upgrade_backend", handleUpgradeBackend},
		{"get_upgrade_status", handleGetUpgradeStatus},
		// #2679: KMS is platform-wide per daemon; the cloud shim answers 501.
		{"kms_status", handleKMSStatus},
		{"kms_envelope_coverage", handleKMSCoverage},
		{"kms_migrate_to_envelope", handleKMSMigrate},
	} {
