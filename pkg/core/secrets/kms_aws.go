package secrets

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Phase 4.1 Phase-C — AWS KMS implementation of KMSClient.
// Audit C-HIGH-6.
//
// AWS KMS exposes Encrypt / Decrypt JSON actions on a
// single regional endpoint; the named key (id, ARN, or
// alias) is the KEK. The key material never leaves AWS's
// HSM-backed boundary — the daemon submits the plaintext
// DEK (base64) and gets back an opaque CiphertextBlob.
//
// We follow the Vault / GCP pattern and talk to the JSON
// REST API directly rather than pulling in aws-sdk-go-v2.
// Reasons:
//
//   - The SDK transitive tree is large (smithy, the whole
//     service-client codegen, the credential-provider
//     chain, …); govulncheck has to track it all, and it
//     dwarfs the rest of this repo's dependency surface.
//   - Our usage is two actions — Encrypt and Decrypt.
//   - The one thing the SDK really buys you is SigV4
//     request signing and the credential chain. SigV4 is
//     ~60 lines of HMAC-SHA256 (below); credentials are
//     supplied by the operator the same way the Vault
//     token and the GCP access token are. Daemons on EC2 /
//     EKS can run a tiny sidecar that refreshes IMDS /
//     IRSA credentials into the env or a file; bare-metal
//     uses a static IAM user. Either way the daemon only
//     needs the access-key / secret pair (+ optional
//     session token for STS temp creds).
//
// Wire shape (AWS JSON 1.1, "TrentService" is KMS's
// internal name):
//
//   POST https://kms.<region>.amazonaws.com/
//     X-Amz-Target: TrentService.Encrypt
//     Authorization: AWS4-HMAC-SHA256 Credential=... Signature=...
//     {"KeyId": "<key>", "Plaintext": "<base64(DEK)>"}
//   →  {"KeyId": "<arn>", "CiphertextBlob": "<base64>"}
//
//   POST https://kms.<region>.amazonaws.com/
//     X-Amz-Target: TrentService.Decrypt
//     {"KeyId": "<key>", "CiphertextBlob": "<base64>"}
//   →  {"KeyId": "<arn>", "Plaintext": "<base64(DEK)>"}
//
// kek_id encodes the region + configured key id so a row
// migrated under one account / key / region can't be
// unwrapped by a daemon reconfigured against a different
// one. AWS's own key rotation is transparent: the key
// version that produced a CiphertextBlob is baked into the
// blob, so Decrypt for old ciphertext keeps succeeding.

// AWSConfig configures the AWS KMS backend.
type AWSConfig struct {
	// Region is the AWS region of the KMS key, e.g.
	// "us-west-2". Used for the endpoint host and the
	// SigV4 credential scope.
	Region string

	// KeyID identifies the KMS key used as the KEK. Accepts
	// any form AWS accepts on Encrypt: a key id
	// ("1234abcd-..."), a full ARN
	// ("arn:aws:kms:us-west-2:111122223333:key/..."), an
	// alias name ("alias/containarium-secrets"), or an
	// alias ARN. Encrypt always uses the key's current
	// rotation; the daemon never pins a version.
	KeyID string

	// AccessKeyID is the AWS access key id (not secret).
	AccessKeyID string

	// SecretAccessKey is the AWS secret access key. Operators
	// refresh this out-of-band (IRSA / IMDS sidecar tee'd to
	// a file, a static IAM user, or Vault's aws secret
	// engine).
	SecretAccessKey string

	// SessionToken is the STS session token, set only when
	// the credentials are temporary (assume-role / IRSA /
	// IMDS). Empty for a static IAM user. When present it is
	// sent as X-Amz-Security-Token and folded into the
	// SigV4 signed headers.
	SessionToken string

	// Endpoint is the KMS API base URL. Defaults to the
	// public regional endpoint; the field exists so tests
	// can point at a httptest.Server and so VPC-endpoint /
	// air-gapped deployments can override.
	Endpoint string

	// Timeout caps every KMS HTTP call. Default 5s.
	Timeout time.Duration
}

// AWSKMS implements KMSClient against AWS KMS.
type AWSKMS struct {
	cfg    AWSConfig
	client *http.Client
	kekID  string // cached, set in NewAWSKMS
}

// awsKEKPrefix labels a kek_id as "wrap was done by AWS
// KMS." Future readers (a daemon swapped to Vault or GCP,
// for instance) match this prefix and refuse to even
// attempt decryption of a non-matching row.
const awsKEKPrefix = "aws:"

// awsKMSService is the SigV4 service name for KMS. Used in
// both the endpoint host and the credential scope.
const awsKMSService = "kms"

// awsJSONContentType is the AWS JSON 1.1 protocol content
// type KMS expects.
const awsJSONContentType = "application/x-amz-json-1.1"

// NewAWSKMS constructs the backend. Validates config shape
// but does NOT call AWS — the first Wrap / Unwrap surfaces a
// bad credential / missing key / network outage as a normal
// Wrap / Unwrap error. Lazy connection mirrors the Vault and
// GCP backends so daemon startup can't be blocked by a
// momentarily-unreachable endpoint.
func NewAWSKMS(cfg AWSConfig) (*AWSKMS, error) {
	cfg.Region = strings.TrimSpace(cfg.Region)
	if cfg.Region == "" {
		return nil, errors.New("aws KMS: region required")
	}
	cfg.KeyID = strings.TrimSpace(cfg.KeyID)
	if cfg.KeyID == "" {
		return nil, errors.New("aws KMS: key id required")
	}
	cfg.AccessKeyID = strings.TrimSpace(cfg.AccessKeyID)
	if cfg.AccessKeyID == "" {
		return nil, errors.New("aws KMS: access key id required")
	}
	if cfg.SecretAccessKey == "" {
		return nil, errors.New("aws KMS: secret access key required")
	}
	cfg.Endpoint = strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	if cfg.Endpoint == "" {
		cfg.Endpoint = fmt.Sprintf("https://kms.%s.amazonaws.com", cfg.Region)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	// kek_id is fully determined by region + configured key
	// id. Rows wrapped under different keys / regions get
	// distinct kek_ids; cross-deployment confusion is
	// structurally impossible. We encode the configured key
	// id (not the ARN echoed back by AWS) so the value is
	// deterministic from config alone — matching how Vault
	// and GCP encode their kek_ids.
	kekID := fmt.Sprintf("%s%s|%s", awsKEKPrefix, cfg.Region, cfg.KeyID)
	return &AWSKMS{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
		kekID:  kekID,
	}, nil
}

// Wrap encrypts the DEK against the configured KMS key.
// AWS returns an opaque base64 CiphertextBlob we store
// verbatim as wrapped_dek; it self-describes the key version
// for Decrypt. kek_id reflects the region + key for
// cross-deployment safety.
func (a *AWSKMS) Wrap(ctx context.Context, plaintextDEK []byte) ([]byte, string, error) {
	if err := checkWrapPlaintext(plaintextDEK); err != nil {
		return nil, "", err
	}
	body := map[string]string{
		"KeyId":     a.cfg.KeyID,
		"Plaintext": base64.StdEncoding.EncodeToString(plaintextDEK),
	}
	var resp struct {
		CiphertextBlob string `json:"CiphertextBlob"`
	}
