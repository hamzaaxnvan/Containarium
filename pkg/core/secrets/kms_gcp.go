package secrets

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Phase 4.1 Phase-C — Google Cloud KMS implementation of
// KMSClient. Audit C-HIGH-6.
//
// Cloud KMS exposes per-key encrypt / decrypt REST
// endpoints; the named CryptoKey is the KEK. The key
// material never leaves Google's HSM-backed boundary —
// the daemon submits the plaintext DEK (base64) and gets
// back an opaque ciphertext blob.
//
// We follow the Vault pattern and talk to the JSON REST
// API directly rather than pulling in the cloud.google.com/
// go/kms SDK. Reasons:
//
//   - The SDK transitive tree is large (gax, grpc, oauth2,
//     metadata, …); govulncheck has to track it all.
//   - Our usage is two endpoints — ~40 lines of HTTP.
//   - Auth is a bearer access token, supplied by the
//     operator the same way the Vault token is. Daemons
//     on GKE/GCE can use a tiny token-refresh sidecar
//     against the metadata server (workload identity);
//     bare-metal can use a static service-account token
//     refreshed by gcloud out-of-band. Either way the
//     daemon only needs the file path.
//
// Wire shape:
//
//   POST <endpoint>/v1/<KEY_NAME>:encrypt
//     Authorization: Bearer <access_token>
//     {"plaintext": "<base64(DEK)>"}
//   →  {"name": "<KEY_NAME>/cryptoKeyVersions/<N>",
//       "ciphertext": "<base64>",
//       "ciphertextCrc32c": "..."}
//
//   POST <endpoint>/v1/<KEY_NAME>:decrypt
//     Authorization: Bearer <access_token>
//     {"ciphertext": "<base64>"}
//   →  {"plaintext": "<base64(DEK)>",
//       "plaintextCrc32c": "..."}
//
// kek_id encodes the full resource name of the KEY VERSION
// the Encrypt response named (#2402) — "gcp:" + the
// CryptoKeyVersion path — so a row migrated under one
// project / key can't be unwrapped by a daemon reconfigured
// against a different one, and so a version that still has
// wrapped values under it can be guarded against destruction.
// Decrypt is always addressed to the CryptoKey (Cloud KMS
// picks the version from the ciphertext); readers that need
// the CryptoKey back out of a kek_id use GCPCryptoKeyFromKEKID,
// which also accepts the pre-#2402 shape (key name only). On
// rotation, decrypt of old ciphertext continues to succeed
// automatically.

// GCPConfig configures the GCP Cloud KMS backend.
type GCPConfig struct {
	// KeyName is the full Cloud KMS resource name of the
	// CryptoKey, e.g.
	// "projects/my-proj/locations/us-west1/keyRings/cont/cryptoKeys/secrets".
	// Use the bare CryptoKey (not a CryptoKeyVersion) —
	// encrypt always uses the key's primary version.
	KeyName string

	// Token is the OAuth2 access token used as
	// `Authorization: Bearer`. Operators refresh this
	// out-of-band (workload identity sidecar / gcloud
	// auth print-access-token tee'd to a file / Vault
	// Agent with the gcp secret engine).
	Token string

	// TokenFile, when set, is a path the backend RE-READS before each Cloud
	// KMS call (cached briefly) so an out-of-band refresh sidecar's rotations
	// are honored without restarting the daemon. Takes precedence over Token.
	// Without it, Token is frozen at startup and expires within the hour. #300.
	TokenFile string

	// Endpoint is the Cloud KMS API base URL. Defaults to
	// the public production endpoint; the field exists so
	// tests can point at a httptest.Server and so
	// air-gapped private-endpoint deployments can override.
	Endpoint string

	// Timeout caps every Cloud KMS HTTP call. Default 5s.
	Timeout time.Duration
}

// GCPKMS implements KMSClient against Google Cloud KMS.
type GCPKMS struct {
	cfg    GCPConfig
	client *http.Client
	// tokMu guards the brief token cache used when cfg.TokenFile is set.
	tokMu     sync.Mutex
	cachedTok string
	cachedAt  time.Time
}

// GCPKEKPrefix labels a kek_id as "wrap was done by GCP
// Cloud KMS." Future readers (an operator who migrated
// from GCP to Vault, for instance) refuse rows that don't
// match their backend's prefix. Exported so callers outside this
// package (internal/secrets.Store's per-tenant KEK dispatch, #1630)
// can parse a kek_id's key resource name back out without duplicating
// the literal.
const GCPKEKPrefix = "gcp:"

// gcpVersionSegment separates a CryptoKey resource name from the version
// suffix Cloud KMS appends in an Encrypt response's `name`.
const gcpVersionSegment = "/cryptoKeyVersions/"

// GCPCryptoKeyFromKEKID returns the CryptoKey resource name a GCP kek_id
// was wrapped under — the thing to address a decrypt to — stripping the
// "gcp:" prefix and any CryptoKeyVersion suffix. ok is false for a kek_id
// that is not GCP's. Accepts both the #2402 shape ("gcp:<key>/
// cryptoKeyVersions/<n>") and rows written before it ("gcp:<key>").
func GCPCryptoKeyFromKEKID(kekID string) (keyName string, ok bool) {
	name, ok := strings.CutPrefix(kekID, GCPKEKPrefix)
	if !ok || name == "" {
		return "", false
	}
	if i := strings.Index(name, gcpVersionSegment); i >= 0 {
		name = name[:i]
	}
	return name, true
}

// gcpDefaultEndpoint is the public Cloud KMS endpoint.
// Override via GCPConfig.Endpoint for private endpoints
// or test doubles.
const gcpDefaultEndpoint = "https://cloudkms.googleapis.com"

// NewGCPKMS constructs the backend. Validates config shape
// but does NOT call Cloud KMS — the first Wrap / Unwrap
// surfaces a bad token / missing key / network outage as a
// normal Wrap / Unwrap error. Lazy connection mirrors the
// Vault backend so daemon startup can't be blocked by a
// momentarily-unreachable KMS endpoint.
func NewGCPKMS(cfg GCPConfig) (*GCPKMS, error) {
	cfg.KeyName = strings.TrimSpace(cfg.KeyName)
	if cfg.KeyName == "" {
		return nil, errors.New("gcp KMS: key name required")
	}
	// Cheap sanity-check the resource path so a typo
	// surfaces at construction time instead of being
	// surfaced as a generic 400 from Cloud KMS on the
	// first Wrap. The full Google resource grammar is
	// stricter than this — we just want the leading
	// shape to match.
	if !strings.HasPrefix(cfg.KeyName, "projects/") ||
		!strings.Contains(cfg.KeyName, "/cryptoKeys/") {
		return nil, fmt.Errorf("gcp KMS: key name %q is not a CryptoKey resource path (want projects/.../cryptoKeys/...)", cfg.KeyName)
	}
