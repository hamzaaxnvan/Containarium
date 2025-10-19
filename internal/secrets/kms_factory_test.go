package secrets

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corecrypto "github.com/footprintai/containarium/pkg/core/secrets"
)

// Phase 4.1 — KMS backend selector tests. Moved here with the factory: the
// env-reading + dispatch now lives in the app layer (internal/secrets), while
// pkg/core/secrets keeps only the env-free *Config structs + constructors.

func clearKMSEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"CONTAINARIUM_KMS_BACKEND",
		"CONTAINARIUM_VAULT_ADDR",
		"CONTAINARIUM_VAULT_TOKEN",
		"CONTAINARIUM_VAULT_TOKEN_FILE",
		"CONTAINARIUM_VAULT_TRANSIT_MOUNT",
		"CONTAINARIUM_VAULT_TRANSIT_KEY",
		"CONTAINARIUM_VAULT_TIMEOUT",
		"CONTAINARIUM_GCP_KMS_KEY_NAME",
		"CONTAINARIUM_GCP_KMS_TOKEN",
		"CONTAINARIUM_GCP_KMS_TOKEN_FILE",
		"CONTAINARIUM_GCP_KMS_ENDPOINT",
		"CONTAINARIUM_GCP_KMS_TIMEOUT",
		"CONTAINARIUM_AWS_KMS_REGION",
		"CONTAINARIUM_AWS_KMS_KEY_ID",
		"CONTAINARIUM_AWS_ACCESS_KEY_ID",
		"CONTAINARIUM_AWS_SECRET_ACCESS_KEY",
		"CONTAINARIUM_AWS_SECRET_ACCESS_KEY_FILE",
		"CONTAINARIUM_AWS_SESSION_TOKEN",
		"CONTAINARIUM_AWS_SESSION_TOKEN_FILE",
		"CONTAINARIUM_AWS_KMS_ENDPOINT",
		"CONTAINARIUM_AWS_KMS_TIMEOUT",
	} {
		t.Setenv(k, "")
	}
}

func mkMaster(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, corecrypto.MasterKeySize)
	if _, err := io.ReadFull(rand.Reader, k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return k
}

func TestLoadKMSClient_DefaultIsNone(t *testing.T) {
	clearKMSEnv(t)
	c, desc, err := LoadKMSClient(mkMaster(t))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if c != nil {
		t.Fatalf("expected nil client when backend unset; got %T", c)
	}
	if desc == "" {
		t.Fatal("expected non-empty description for the disabled path")
	}
}

func TestLoadKMSClient_NoneIsExplicitlyDisabled(t *testing.T) {
	clearKMSEnv(t)
	t.Setenv("CONTAINARIUM_KMS_BACKEND", "none")
	c, _, err := LoadKMSClient(mkMaster(t))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if c != nil {
		t.Fatal("expected nil client")
	}
}

func TestLoadKMSClient_InProcReturnsImpl(t *testing.T) {
	clearKMSEnv(t)
	t.Setenv("CONTAINARIUM_KMS_BACKEND", "inproc")
	c, desc, err := LoadKMSClient(mkMaster(t))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if _, ok := c.(*corecrypto.InProcKMS); !ok {
		t.Fatalf("expected *InProcKMS; got %T", c)
	}
	if desc == "" {
		t.Fatal("expected description")
	}
}

func TestLoadKMSClient_VaultRequiresAddr(t *testing.T) {
	clearKMSEnv(t)
	t.Setenv("CONTAINARIUM_KMS_BACKEND", "vault")
	// No address set.
	_, _, err := LoadKMSClient(mkMaster(t))
	if err == nil {
		t.Fatal("vault backend without CONTAINARIUM_VAULT_ADDR should error")
	}
}

func TestLoadKMSClient_VaultRequiresKey(t *testing.T) {
	clearKMSEnv(t)
	t.Setenv("CONTAINARIUM_KMS_BACKEND", "vault")
	t.Setenv("CONTAINARIUM_VAULT_ADDR", "http://vault.local:8200")
	t.Setenv("CONTAINARIUM_VAULT_TOKEN", "t")
	_, _, err := LoadKMSClient(mkMaster(t))
	if err == nil {
		t.Fatal("vault backend without CONTAINARIUM_VAULT_TRANSIT_KEY should error")
	}
}

func TestLoadKMSClient_VaultRequiresToken(t *testing.T) {
	clearKMSEnv(t)
	t.Setenv("CONTAINARIUM_KMS_BACKEND", "vault")
	t.Setenv("CONTAINARIUM_VAULT_ADDR", "http://vault.local:8200")
	t.Setenv("CONTAINARIUM_VAULT_TRANSIT_KEY", "k")
	_, _, err := LoadKMSClient(mkMaster(t))
	if err == nil {
		t.Fatal("vault backend without token env or file should error")
	}
}

func TestLoadKMSClient_VaultTokenFromFile(t *testing.T) {
