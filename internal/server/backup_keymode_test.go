package server

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/backup"
	"github.com/footprintai/containarium/pkg/core/secrets"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// --- #2402: managed key mode at the server boundary ---

const testBackupKMSKey = "projects/test-proj/locations/us-west1/keyRings/r/cryptoKeys/backups"

// clearBackupKMSEnv unsets every variable NewBackupServer's wrapper
// loading reads, so a developer's shell cannot leak into the table.
func clearBackupKMSEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"CONTAINARIUM_BACKUP_KMS_KEY_NAME", "CONTAINARIUM_KMS_BACKEND",
		"CONTAINARIUM_GCP_KMS_KEY_NAME", "CONTAINARIUM_GCP_KMS_TOKEN",
		"CONTAINARIUM_GCP_KMS_TOKEN_FILE", "CONTAINARIUM_GCP_KMS_ENDPOINT",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("CONTAINARIUM_BACKUP_DIR", t.TempDir())
}

// The wrapper is built from CONTAINARIUM_BACKUP_KMS_KEY_NAME via the same
// resource-name keyed factory the secrets store uses for per-tenant KEKs.
// Empty means no wrapper (today's behaviour). Set but unloadable — the
// KMS backend is not gcp, or the name is not a CryptoKey — is a startup
// error: an operator who configured managed backups must never get
// silent legacy backups instead.
func TestNewBackupServer_WrapperFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name        string
		env         map[string]string
		wantWrapper bool
		wantErr     string
	}{
		{
			name: "unset: no wrapper, legacy behaviour",
		},
		{
			name: "set with a gcp backend: wrapper built",
			env: map[string]string{
				"CONTAINARIUM_BACKUP_KMS_KEY_NAME": testBackupKMSKey,
				"CONTAINARIUM_KMS_BACKEND":         "gcp",
				"CONTAINARIUM_GCP_KMS_KEY_NAME":    "projects/test-proj/locations/us-west1/keyRings/r/cryptoKeys/secrets",
				"CONTAINARIUM_GCP_KMS_TOKEN":       "tok",
			},
			wantWrapper: true,
		},
		{
			name: "set but no KMS backend configured",
			env: map[string]string{
				"CONTAINARIUM_BACKUP_KMS_KEY_NAME": testBackupKMSKey,
			},
			wantErr: "CONTAINARIUM_BACKUP_KMS_KEY_NAME",
		},
		{
			name: "set but the KMS backend is not gcp",
			env: map[string]string{
				"CONTAINARIUM_BACKUP_KMS_KEY_NAME": testBackupKMSKey,
				"CONTAINARIUM_KMS_BACKEND":         "inproc",
			},
			wantErr: "CONTAINARIUM_BACKUP_KMS_KEY_NAME",
		},
		{
			name: "malformed key name",
			env: map[string]string{
				"CONTAINARIUM_BACKUP_KMS_KEY_NAME": "not/a/crypto/key",
				"CONTAINARIUM_KMS_BACKEND":         "gcp",
				"CONTAINARIUM_GCP_KMS_KEY_NAME":    "projects/test-proj/locations/us-west1/keyRings/r/cryptoKeys/secrets",
				"CONTAINARIUM_GCP_KMS_TOKEN":       "tok",
			},
			wantErr: "CryptoKey",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearBackupKMSEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			s, err := NewBackupServer(&ContainerServer{})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("NewBackupServer err = %v, want one mentioning %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewBackupServer: %v", err)
			}
			if got := s.Manager().HasWrapper(); got != tc.wantWrapper {
				t.Errorf("HasWrapper = %v, want %v", got, tc.wantWrapper)
			}
		})
	}
}

func TestKeyModeFromProto(t *testing.T) {
	for _, tc := range []struct {
		name         string
		requested    pb.BackupKeyMode
		hasWrapper   bool
		hasRecipient bool
		want         backup.KeyMode
		wantCode     codes.Code
	}{
		{"unspecified is the daemon default", pb.BackupKeyMode_BACKUP_KEY_MODE_UNSPECIFIED, false, false, "", codes.OK},
		{"unspecified with a wrapper is still the daemon default", pb.BackupKeyMode_BACKUP_KEY_MODE_UNSPECIFIED, true, true, "", codes.OK},
		{"age_recipient with a recipient", pb.BackupKeyMode_BACKUP_KEY_MODE_AGE_RECIPIENT, false, true, backup.KeyModeAgeRecipient, codes.OK},
		{"age_recipient without a recipient", pb.BackupKeyMode_BACKUP_KEY_MODE_AGE_RECIPIENT, true, false, "", codes.InvalidArgument},
		{"managed with a wrapper", pb.BackupKeyMode_BACKUP_KEY_MODE_MANAGED, true, false, backup.KeyModeManaged, codes.OK},
		{"managed without a wrapper", pb.BackupKeyMode_BACKUP_KEY_MODE_MANAGED, false, true, "", codes.FailedPrecondition},
		{"both with both", pb.BackupKeyMode_BACKUP_KEY_MODE_BOTH, true, true, backup.KeyModeBoth, codes.OK},
		{"both without a wrapper", pb.BackupKeyMode_BACKUP_KEY_MODE_BOTH, false, true, "", codes.FailedPrecondition},
		{"both without a recipient", pb.BackupKeyMode_BACKUP_KEY_MODE_BOTH, true, false, "", codes.InvalidArgument},
		{"a value this build does not know", pb.BackupKeyMode(99), true, true, "", codes.InvalidArgument},
