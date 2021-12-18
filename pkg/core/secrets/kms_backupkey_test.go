package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- #2402: KMSClient as the backup key wrapper ---
//
// Two contract changes the managed-backup design relies on:
//
//  1. kek_id from the GCP backend is the CryptoKeyVersion resource name the
//     Encrypt response carries, not the CryptoKey the client was configured
//     with — so destruction of a key version can be guarded by "are there
//     backups wrapped under it".
//  2. Wrap accepts a small secret of any length (an age identity string is
//     ~74 bytes), not only a 32-byte DEK.

// recordedEncryptResponse is the shape Cloud KMS actually returns from
// cryptoKeys.encrypt: the `name` is the key VERSION used.
const recordedKeyVersion = testKeyName + "/cryptoKeyVersions/7"

// recordedGCPKMS serves one recorded :encrypt response and a :decrypt that
// echoes back whatever was last encrypted. It also records every request
// path so the test can assert which resource the client addressed.
func recordedGCPKMS(t *testing.T, encryptName string) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	var lastPlaintext string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, ":encrypt"):
			var req struct {
				Plaintext string `json:"plaintext"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			lastPlaintext = req.Plaintext
			resp := map[string]any{
				"ciphertext":       "CiQAZ3ZmV2hhdGV2ZXIgY2lwaGVydGV4dCBieXRlcyBoZXJlEkQAbWFkZQ==",
				"ciphertextCrc32c": "3522255473",
				"protectionLevel":  "SOFTWARE",
			}
			if encryptName != "" {
				resp["name"] = encryptName
			}
			_ = json.NewEncoder(w).Encode(resp)
		case strings.HasSuffix(r.URL.Path, ":decrypt"):
			_ = json.NewEncoder(w).Encode(map[string]any{"plaintext": lastPlaintext, "plaintextCrc32c": "0"})
		default:
			http.Error(w, `{"error":{"message":"unknown op"}}`, http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}
