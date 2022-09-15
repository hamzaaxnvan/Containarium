package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// Phase 4.1 Phase-A — KMS envelope-encryption interface +
// in-process no-op impl. Audit C-HIGH-6. See
// docs/security/KMS-ENVELOPE-DESIGN.md for the threat
// model and the multi-phase rollout plan.
//
// THIS PR INTRODUCES THE INTERFACE ONLY. No callers wire
// it yet — the existing Cipher path remains the canonical
// encrypt/decrypt route. Subsequent phases wire envelope
// rows alongside the legacy ciphertext (Phase B), add
// real KMS backends (Phase C), and eventually retire the
// master key (Phase E).
//
// Wire shape: every secret carries a per-row Data
// Encryption Key (DEK). The DEK encrypts the plaintext via
// AES-GCM the same way the master key does today. The
// KMSClient.Wrap call encrypts the DEK under the
// KMS-resident Key Encryption Key (KEK). Storage holds
// (ciphertext, wrapped_dek, kek_id). On read, KMSClient.Unwrap
// turns the wrapped DEK back into a plaintext DEK in
// daemon memory; the daemon decrypts the value and zeroes
// the DEK before returning.

// KMSClient is the narrow envelope-encryption interface
// the Store talks to. Implementations:
//
//   - kms_inproc.go (this file): no-op shim using the
//     existing master key. Default for backwards compat;
//     gets the schema migration in place without changing
//     the cryptographic protection.
//   - kms_gcp.go (future): GCP KMS via cloudkms.v1.
//   - kms_vault.go (future): Vault Transit secret engine.
//   - kms_aws.go (future): AWS KMS.
//
// The interface is intentionally tight: just Wrap and
// Unwrap. Backends that need more state (versioning,
// rotation hints) carry it inside the implementation, not
// the contract.
type KMSClient interface {
	// Wrap encrypts a plaintext Data Encryption Key (DEK)
	// under the KMS-resident Key Encryption Key (KEK).
	// Returns the wrapped DEK and the KEK identifier
	// (provider-specific — for GCP KMS it's the full
	// CryptoKeyVersion resource name the Encrypt response
	// named, so a key version with wrapped values still
	// under it can be guarded against destruction (#2402);
	// for the inproc impl it's a sentinel). The wrapped DEK
	// lands in the secrets row alongside the ciphertext.
	//
	// The plaintext is a small secret: a 32-byte DEK for the
	// secrets store, or a backup key identity string for the
	// managed-backup wrapper (#2402). Implementations accept
	// 1..MaxWrapPlaintext bytes.
	Wrap(ctx context.Context, plaintextDEK []byte) (wrappedDEK []byte, kekID string, err error)

	// Unwrap reverses Wrap. The kekID is the value returned
	// by the original Wrap call — implementations use it to
	// pick the right key version on the backend. Returns
	// the plaintext DEK; callers MUST zero it after use.
	Unwrap(ctx context.Context, wrappedDEK []byte, kekID string) (plaintextDEK []byte, err error)
}

// DEKSize is the size of a Data Encryption Key. AES-256
// matches the existing master-key path.
const DEKSize = 32

// MaxWrapPlaintext bounds what Wrap accepts: the smallest
// per-call limit among the backends (AWS KMS Encrypt takes
// at most 4 KiB), applied uniformly so a value wrapped
// under one backend is never too large for another.
const MaxWrapPlaintext = 4096

// checkWrapPlaintext is the guard every backend's Wrap
// applies: non-empty, and no larger than MaxWrapPlaintext.
func checkWrapPlaintext(plaintext []byte) error {
	if len(plaintext) == 0 {
		return errors.New("wrap: plaintext is empty")
	}
	if len(plaintext) > MaxWrapPlaintext {
		return fmt.Errorf("wrap: plaintext is %d bytes; max %d", len(plaintext), MaxWrapPlaintext)
	}
	return nil
}

// inprocKEKID is the sentinel kek_id stored by the in-
// process KMS. Future per-row reads use this to route to
// the inproc Unwrap; real KMS rows carry a real resource
// path.
const inprocKEKID = "inproc:master"

// InProcKMS is the no-op envelope backend. It wraps DEKs
// using the daemon's existing master key with AES-GCM.
// Cryptographically equivalent to the legacy single-key
// path — the protection level is the same, the storage
// shape is just one indirection longer. The point is to
// land the schema and the Store-side plumbing without
// changing the threat model; switching to a real KMS later
// is then a configuration change, not a code change at
// every callsite.
type InProcKMS struct {
	aead cipher.AEAD
}

// NewInProcKMS builds an InProcKMS from the daemon's
// master key (the same 32-byte key Cipher uses). The KMS
// uses a different AEAD instance than Cipher so that even
// a future bug in one path can't pollute the other.
func NewInProcKMS(masterKey []byte) (*InProcKMS, error) {
	if len(masterKey) != MasterKeySize {
		return nil, ErrKeyWrongSize
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}
	return &InProcKMS{aead: aead}, nil
}

// Wrap encrypts the DEK under the master key with a fresh
