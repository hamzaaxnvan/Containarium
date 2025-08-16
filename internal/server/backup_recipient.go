package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/footprintai/containarium/internal/secrets"
)

// backupAgeRecipientSecretName is the well-known tenant-secret name a
// tenant self-registers its backup age recipient under (#1836). It rides
// the existing Secrets API — versioned, audited, and eligible for a
// per-tenant KMS-backed KEK via SetTenantKMSKey — rather than a second,
// bespoke config store. The value is a PUBLIC key: storing it durably
// costs nothing in confidentiality, and doing so here means a scheduled
// backup needs no operator present to pass --age-recipient on every run.
// The matching PRIVATE identity must never be registered here, and never
// touches the platform — see docs/DB-BACKUP-OPERATIONS.md.
const backupAgeRecipientSecretName = "CONTAINARIUM_BACKUP_AGE_RECIPIENT" // #nosec G101 -- secret NAME, not a credential value

// secretsReader is the subset of *secrets.Store CreateBackup needs to
// resolve a tenant's registered recipient. A narrow interface — not the
// whole store — keeps resolveAgeRecipient unit-testable without a real
// Postgres + KMS backend.
type secretsReader interface {
	Get(ctx context.Context, username, name string) (*secrets.SecretMetadata, string, error)
}

// resolveAgeRecipient decides what age recipient (if any) a backup
// should be encrypted to (#1836):
//  1. explicit (the request's own age_recipient field) always wins — a
//     one-off call overrides whatever the tenant has registered.
//  2. otherwise, the tenant's own CONTAINARIUM_BACKUP_AGE_RECIPIENT
//     secret, if one is registered.
//  3. otherwise "" — plaintext, exactly today's default. Neither a
//     missing secrets store (standalone daemon) nor a tenant who never
