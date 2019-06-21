package secrets

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/config"
	corecrypto "github.com/footprintai/containarium/pkg/core/secrets"
)

// Phase 4.1 — KMS backend selector. Audit C-HIGH-6.
//
// The daemon and the migration CLI both need to build a KMSClient based on
// operator configuration. This factory lives in the app layer (not in
// pkg/core/secrets) so the core crypto package stays free of environment
// reading: it exposes only the typed *Config structs + constructors, and this
// file reads the environment (via internal/config name constants), resolves
// credential files, validates, and dispatches to the right backend.

// Recognized backend names. The default is "none" — no KMS configured, behavior
// identical to pre-Phase-4.1. Operators opt in by setting CONTAINARIUM_KMS_BACKEND.
const (
	KMSBackendNone   = "none"
	KMSBackendInProc = "inproc"
	KMSBackendVault  = "vault"
	KMSBackendGCP    = "gcp"
	KMSBackendAWS    = "aws"
)

// LoadKMSClient picks a backend based on CONTAINARIUM_KMS_BACKEND and returns a
// constructed KMSClient plus a human-readable description for the startup log.
// Returns (nil, "disabled", nil) when the backend is "none" or the env var is
// unset.
//
// masterKey is the daemon's existing master key from LoadOrCreateMasterKey. The
// InProc backend wraps DEKs against it (cryptographically equivalent to legacy);
// other backends ignore it (Vault wraps via its KMS-resident Transit key).
func LoadKMSClient(masterKey []byte) (corecrypto.KMSClient, string, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv(config.EnvKMSBackend)))
	if backend == "" {
		backend = KMSBackendNone
	}
	switch backend {
	case KMSBackendNone, "off", "disabled":
		return nil, "disabled (CONTAINARIUM_KMS_BACKEND=none)", nil
	case KMSBackendInProc:
		k, err := corecrypto.NewInProcKMS(masterKey)
		if err != nil {
			return nil, "", fmt.Errorf("KMS backend inproc: %w", err)
		}
		return k, "inproc (master-key envelope, dev/test)", nil
	case KMSBackendVault:
		cfg, err := vaultConfigFromEnv()
		if err != nil {
			return nil, "", fmt.Errorf("KMS backend vault: %w", err)
		}
		k, err := corecrypto.NewVaultKMS(cfg)
		if err != nil {
			return nil, "", fmt.Errorf("KMS backend vault: %w", err)
		}
		return k, fmt.Sprintf("vault transit (addr=%s mount=%s key=%s)",
			cfg.Address, cfg.Mount, cfg.KeyName), nil
	case KMSBackendGCP:
		cfg, err := gcpConfigFromEnv()
		if err != nil {
			return nil, "", fmt.Errorf("KMS backend gcp: %w", err)
		}
		k, err := corecrypto.NewGCPKMS(cfg)
		if err != nil {
			return nil, "", fmt.Errorf("KMS backend gcp: %w", err)
		}
		return k, fmt.Sprintf("gcp cloud kms (key=%s)", cfg.KeyName), nil
	case KMSBackendAWS:
		cfg, err := awsConfigFromEnv()
		if err != nil {
			return nil, "", fmt.Errorf("KMS backend aws: %w", err)
		}
		k, err := corecrypto.NewAWSKMS(cfg)
		if err != nil {
			return nil, "", fmt.Errorf("KMS backend aws: %w", err)
		}
		return k, fmt.Sprintf("aws kms (region=%s key=%s)", cfg.Region, cfg.KeyID), nil
	default:
		return nil, "", fmt.Errorf("KMS backend: unrecognized value %q (expected: none, inproc, vault, gcp, aws)", backend)
	}
}

// TenantKMSFactory builds a KMSClient scoped to one caller-supplied key
// resource name, reusing the daemon's own KMS auth config (token/token
// file/endpoint/timeout). Returns an error if the resource name is
// malformed for the backend.
type TenantKMSFactory func(keyResourceName string) (corecrypto.KMSClient, error)

// LoadTenantKMSFactory returns a TenantKMSFactory for per-tenant KEKs
// (#1630) — platform-managed keys in the SAME GCP project the daemon's
// shared KEK lives in, not cross-project BYOK (that's a later,
// separately-scoped feature).
//
// Returns (nil, nil) when CONTAINARIUM_KMS_BACKEND isn't "gcp": per-tenant
// keys aren't supported for the other backends yet, and a nil factory is
// how internal/secrets.Store knows to reject SetTenantKMSKey up front
// rather than silently no-op it.
func LoadTenantKMSFactory() (TenantKMSFactory, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv(config.EnvKMSBackend)))
	if backend != KMSBackendGCP {
		return nil, nil
	}
	cfg, err := gcpConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("KMS backend gcp: %w", err)
	}
	return func(keyResourceName string) (corecrypto.KMSClient, error) {
		perTenant := cfg
		perTenant.KeyName = keyResourceName
		return corecrypto.NewGCPKMS(perTenant)
	}, nil
}

// vaultConfigFromEnv reads the Vault Transit config from env. Required:
// CONTAINARIUM_VAULT_ADDR, CONTAINARIUM_VAULT_TOKEN (or _TOKEN_FILE),
// CONTAINARIUM_VAULT_TRANSIT_KEY. Optional: CONTAINARIUM_VAULT_TRANSIT_MOUNT
// (default "transit"), CONTAINARIUM_VAULT_TIMEOUT (default 5s).
func vaultConfigFromEnv() (corecrypto.VaultConfig, error) {
	cfg := corecrypto.VaultConfig{
		Address: strings.TrimSpace(os.Getenv(config.EnvVaultAddr)),
		Mount:   strings.TrimSpace(os.Getenv(config.EnvVaultTransitMount)),
		KeyName: strings.TrimSpace(os.Getenv(config.EnvVaultTransitKey)),
	}
	if cfg.Address == "" {
		return cfg, fmt.Errorf("CONTAINARIUM_VAULT_ADDR is required")
	}
	if cfg.KeyName == "" {
		return cfg, fmt.Errorf("CONTAINARIUM_VAULT_TRANSIT_KEY is required")
	}

	// Token: env wins over file. Either is fine; file is the recommended path for
	// long-lived daemons (Vault Agent writes a fresh token there and renews it).
	if tok := strings.TrimSpace(os.Getenv(config.EnvVaultToken)); tok != "" {
		cfg.Token = tok
	} else if path := strings.TrimSpace(os.Getenv(config.EnvVaultTokenFile)); path != "" {
