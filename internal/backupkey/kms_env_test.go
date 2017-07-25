package backupkey

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An operator machine names the key through the backup record's
// kek_id, not through CONTAINARIUM_GCP_KMS_KEY_NAME (which is the daemon's
// shared KEK). Backend + token are all the operator sets.
func TestLoadKMSClientForKey_GCPNeedsNoSharedKeyName(t *testing.T) {
	clearOperatorKMSEnv(t)
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]string{"plaintext": base64.StdEncoding.EncodeToString([]byte("pt"))})
	}))
	defer srv.Close()
	t.Setenv("CONTAINARIUM_KMS_BACKEND", "gcp")
	t.Setenv("CONTAINARIUM_GCP_KMS_TOKEN", "operator-token")
	t.Setenv("CONTAINARIUM_GCP_KMS_ENDPOINT", srv.URL)
