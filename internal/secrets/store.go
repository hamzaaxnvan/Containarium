// Package secrets implements the daemon-side Postgres-backed store
// for tenant secrets, layered on top of pkg/core/secrets crypto.
// See docs/SECRETS-MANAGEMENT-DESIGN.md.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	corecrypto "github.com/footprintai/containarium/pkg/core/secrets"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SecretMetadata is the public-safe view of a stored secret —
// matches the proto message of the same name. The plaintext value
// lives only in memory during Get and never in this struct.
type SecretMetadata struct {
	Username  string
	Name      string
	Version   int32
	CreatedAt time.Time
	UpdatedAt time.Time

	// Phase 4.3 — delivery mode: "env", "file" (default since #1604), or
	// "compose". Phase A lands the field; Phase B switches the stamping
	// path to honor it. See docs/security/SECRETS-ENV-VAR-RISK.md.
	Delivery string
}

// Delivery-mode constants. The DB column stores these
// strings literally; new values land here before the
// schema is taught to validate them.
const (
	DeliveryEnv  = "env"
	DeliveryFile = "file"
	// DeliveryCompose writes the secret into a shared dotenv file
	// (/run/containarium/secrets.env) that nested docker /
	// docker-compose apps consume via `env_file:` — they don't
	// inherit the LXC's Incus-config environment (the same gap OTel
	// solved, #370/#492). Values must be single-line; see Set.
	DeliveryCompose = "compose"
	// DeliveryBroker marks a secret held for the daemon's own brokered
	// upstream calls (e.g. the tracker broker's forge credential, #1921)
	// — never delivered to any box, and readable back only through
	// BrokerCredential. See docs/architecture/agent-tracker-broker.md.
	DeliveryBroker = "broker"
)

// ValidateDelivery returns nil for "" (defaults to file at the storage
// layer — #1604), "env", "file", "compose", or "broker". Anything else
// is caller-error and rejected at the API boundary.
func ValidateDelivery(mode string) error {
	switch mode {
	case "", DeliveryEnv, DeliveryFile, DeliveryCompose, DeliveryBroker:
		return nil
	}
	return fmt.Errorf("secrets: delivery must be %q, %q, %q, or %q; got %q",
		DeliveryEnv, DeliveryFile, DeliveryCompose, DeliveryBroker, mode)
}

// ValidateValueForDelivery rejects a value that the chosen delivery mode
// can't represent. compose renders into a dotenv file (KEY=value lines),
// so a value with a newline would corrupt the file / be mangled by the
// `env_file:` parser — reject it at set-time and point at "file" delivery
// (per-secret tmpfs) for multi-line blobs. Pure function for testing.
func ValidateValueForDelivery(delivery, value string) error {
	if delivery == DeliveryCompose && strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("secrets: delivery %q requires a single-line value (no newlines); use %q delivery for multi-line secrets",
			DeliveryCompose, DeliveryFile)
	}
	return nil
}

// Store handles per-tenant secret persistence.
//
// Two encryption modes coexist on the same table (Phase 4.1 — see
// docs/security/KMS-ENVELOPE-DESIGN.md):
//
//   - Legacy: the row's nonce + ciphertext are AES-256-GCM under
//     the daemon's master key directly. wrapped_dek IS NULL,
//     kek_id IS NULL.
//   - Envelope: the row's nonce + ciphertext are AES-256-GCM
//     under a per-row Data Encryption Key (DEK). The DEK itself
//     is encrypted under the KMS-resident Key Encryption Key
//     (KEK) and stored in wrapped_dek; the KEK identifier is in
//     kek_id.
//
// Whether a Set produces a legacy or envelope row depends on
// whether the Store was constructed with a KMSClient. Sets
// without KMS write legacy rows; Sets with KMS write envelope
// rows. Get/LoadAllForUser dispatches per-row based on whether
// wrapped_dek IS NULL — so a deployment can run with mixed
// state (legacy rows from before the KMS rollout + new envelope
// rows) until Phase D's migration tool converts everything.
type Store struct {
	pool   *pgxpool.Pool
	cipher *corecrypto.Cipher
	kms    corecrypto.KMSClient // optional; nil = legacy-only mode

	// Phase 4.1 Phase-E — when true, the Store refuses
	// to decrypt any row whose wrapped_dek IS NULL.
	// Combined with KMS configured, this is the post-
	// retirement contract: every secret MUST be in
	// envelope form. Operators flip this after the
	// migrator reports 100% coverage; from that point
	// on, a legacy row hitting Get is a strong "you
	// missed a migration" signal that should page.
	requireEnvelope bool

	// #1630 — per-tenant KEK override. tenantKMSFactory is nil unless
	// the daemon's KMS backend is "gcp" (LoadTenantKMSFactory); a nil
	// factory means SetTenantKMSKey refuses rather than silently
	// no-op-ing.
	//
	// tenantKEK maps username -> the GCP key resource name that
	// username's NEW writes should wrap under (in-memory cache of the
	// tenant_kms_keys table, loaded once at construction and kept in
	// sync by SetTenantKMSKey/ClearTenantKMSKey).
	//
	// kekClients caches built KMSClient instances by key resource
	// name — shared between the encrypt path (resolveEncryptKMS,
	// keyed by tenantKEK) and the decrypt path (resolveDecryptKMS,
	// keyed by parsing the ROW's own kek_id). Decrypt being resolved
	// per-row rather than per-"current override" is what makes a
	// partially-completed SetTenantKMSKey/ClearTenantKMSKey safe: a
	// row still carrying its old kek_id keeps decrypting correctly
	// even while other rows for the same tenant have already moved to
	// the new key.
	tenantKMSFactory TenantKMSFactory
	tenantKEKMu      sync.RWMutex
	tenantKEK        map[string]string
	kekClientsMu     sync.RWMutex
	kekClients       map[string]corecrypto.KMSClient
}

// ErrNotFound is returned by Get / Delete when the (username, name)
// tuple has no row.
var ErrNotFound = errors.New("secrets: not found")

// ErrTenantKMSNotSupported is returned by SetTenantKMSKey when the
// daemon has no tenantKMSFactory configured (#1630) — i.e.
// CONTAINARIUM_KMS_BACKEND isn't "gcp". Callers map this to a
// caller-facing precondition failure, not an internal-error catch-all.
var ErrTenantKMSNotSupported = errors.New("secrets: per-tenant KMS keys require CONTAINARIUM_KMS_BACKEND=gcp")

// ErrBrokerOnly is returned by Get for a broker-mode row: the write-only
// guarantee means Get never returns the plaintext, regardless of the
// caller's scopes. The value is reachable only through BrokerCredential.
var ErrBrokerOnly = errors.New("secrets: broker-only secret; value is not readable via Get")

// ErrNotBrokerOnly is returned by BrokerCredential for a row that is NOT
// in broker mode — it refuses to hand back a value from any delivering
// mode, which would defeat the point of a separate, narrowly-wired read
// path for brokered credentials.
var ErrNotBrokerOnly = errors.New("secrets: BrokerCredential refuses a secret that is not broker-only")

// ErrBrokerModeImmutable is returned by Set when a request would change
// an existing broker-only secret's delivery mode to a delivering one.
// One-way by design: delete and re-set is the only path, so the
// write-only guarantee can't be undone by an ordinary SetSecret call.
var ErrBrokerModeImmutable = errors.New("secrets: cannot change a broker-only secret to a delivering mode; delete and re-set instead")

// Option configures a Store at construction time. Phase 4.1 uses
// this to bolt on the KMS client without breaking the existing
// NewStore(ctx, pool, cipher) call sites.
type Option func(*Store)

// WithKMS enables envelope encryption. When set, every new Set
// produces an envelope row (wrapped_dek + kek_id populated).
// Reads accept both legacy and envelope rows.
//
// Passing nil is a no-op — equivalent to omitting WithKMS.
func WithKMS(kms corecrypto.KMSClient) Option {
	return func(s *Store) {
		if kms != nil {
			s.kms = kms
		}
	}
}

// WithRequireEnvelope enforces Phase-E retirement: every
// read MUST go through the envelope path. Legacy rows
// (wrapped_dek IS NULL) are rejected at Get / LoadAllForUser.
// Operators flip this on once `containarium secrets
// envelope-coverage` reports legacy=0 — at that point the
// master key is unused for production decrypts and the
// keyfile can be retired.
//
// Pairs with the daemon-side startup gate that refuses to
// start when require_envelope=true but no KMS backend is
// configured.
func WithRequireEnvelope(require bool) Option {
	return func(s *Store) {
		s.requireEnvelope = require
	}
}

// WithTenantKMSFactory enables per-tenant KEKs (#1630). Pass the result
// of secrets.LoadTenantKMSFactory() — nil is a no-op, equivalent to
// omitting this option, matching WithKMS's convention.
func WithTenantKMSFactory(f TenantKMSFactory) Option {
	return func(s *Store) {
		if f != nil {
			s.tenantKMSFactory = f
		}
	}
}

// NewStore opens the secrets store. Creates the `secrets` table on
// first run and applies any column migrations; idempotent on every
// subsequent call.
//
// The cipher must already be constructed from the daemon's master
// key (see pkg/core/secrets.LoadOrCreateMasterKey + NewCipher) —
// it's the LEGACY-path crypto and stays required even when
// WithKMS is supplied, because existing legacy rows still need it
// for decrypt until Phase D's migration runs.
func NewStore(ctx context.Context, pool *pgxpool.Pool, cipher *corecrypto.Cipher, opts ...Option) (*Store, error) {
	if pool == nil {
		return nil, errors.New("secrets: pool is nil")
	}
	if cipher == nil {
		return nil, errors.New("secrets: cipher is nil")
	}
	s := &Store{
		pool:       pool,
		cipher:     cipher,
		tenantKEK:  map[string]string{},
		kekClients: map[string]corecrypto.KMSClient{},
	}
	for _, opt := range opts {
		opt(s)
	}
	if err := s.initSchema(ctx); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}
	if err := s.loadTenantKEKCache(ctx); err != nil {
		return nil, fmt.Errorf("load tenant kms key overrides: %w", err)
	}
	return s, nil
}

func (s *Store) initSchema(ctx context.Context) error {
	schema := `
		CREATE TABLE IF NOT EXISTS secrets (
			id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			username     TEXT NOT NULL,
			name         TEXT NOT NULL,
			nonce        BYTEA NOT NULL,
			ciphertext   BYTEA NOT NULL,
			version      INT  NOT NULL DEFAULT 1,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (username, name)
		);

		-- Phase 4.1 Phase B (audit C-HIGH-6) — envelope encryption.
		-- Both nullable so the migration is non-destructive: pre-
		-- KMS rows keep wrapped_dek=NULL / kek_id=NULL and are
		-- decrypted via the legacy master-key path. New writes
		-- under KMS populate both columns.
		ALTER TABLE secrets ADD COLUMN IF NOT EXISTS wrapped_dek BYTEA;
		ALTER TABLE secrets ADD COLUMN IF NOT EXISTS kek_id      TEXT;

		-- Phase 4.3 Phase A — delivery mode column.
		-- "env" or "file"; defaults to "env" so pre-4.3 rows
		-- and any future row that omits the field behave
		-- exactly as before. Phase B switches the stamping
		-- code to honor this value.
		ALTER TABLE secrets ADD COLUMN IF NOT EXISTS delivery TEXT NOT NULL DEFAULT 'env';

		CREATE INDEX IF NOT EXISTS idx_secrets_username
			ON secrets(username);

		-- #1630 — per-tenant KEK overrides. One row per tenant that has
		-- opted into its own key; absence means the tenant is on the
		-- shared KEK. No soft-delete: clearing the override is a hard
		-- DELETE, matching org_encryption_settings-style tables — the
		-- audit log carries the durable history, not this row.
		CREATE TABLE IF NOT EXISTS tenant_kms_keys (
			username          TEXT PRIMARY KEY,
			kek_resource_name TEXT NOT NULL,
			created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`
	_, err := s.pool.Exec(ctx, schema)
	return err
}

// Set creates or updates a secret. Idempotent — repeated calls with
// the same (username, name) bump the version and replace the
// ciphertext. Validates name + value at the API boundary before
// touching crypto or the DB.
//
// When the Store has a KMSClient configured (WithKMS), this writes
// an envelope row: a fresh per-row DEK encrypts the plaintext, the
// DEK is wrapped via the KMS, and wrapped_dek + kek_id are
// populated. Otherwise it writes a legacy row exactly as before
// Phase 4.1 — wrapped_dek and kek_id stay NULL.
//
// `delivery` (Phase 4.3) is one of "" (defaults to file on storage — #1604),
// "env", "file", "compose". Validated at the API boundary; invalid values
// reject before any DB work.
//
// The reserved model-gateway namespace is refused here (see gateway_keys.go):
// per-owner gateway keys are written only through SetGatewayProviderKey, so no
// RPC that forwards a caller-supplied username can plant or overwrite one.
func (s *Store) Set(ctx context.Context, username, name, value, delivery string) (*SecretMetadata, error) {
	if IsGatewayKeyNamespace(username) {
		return nil, fmt.Errorf("%w: %q is the model gateway's own; use SetGatewayProviderKey", ErrReservedNamespace, username)
	}
	return s.set(ctx, username, name, value, delivery)
}

// set is Set without the reserved-namespace guard — the one path that may write
// into it (SetGatewayProviderKey).
func (s *Store) set(ctx context.Context, username, name, value, delivery string) (*SecretMetadata, error) {
	if username == "" {
		return nil, fmt.Errorf("username is required")
	}
	if err := corecrypto.ValidateName(name); err != nil {
		return nil, err
	}
	if err := corecrypto.ValidateValue(value); err != nil {
		return nil, err
	}
	if err := ValidateDelivery(delivery); err != nil {
		return nil, err
	}
	// compose delivery renders into a dotenv file; reject multi-line
	// values at set-time rather than silently corrupt it (#491).
	if err := ValidateValueForDelivery(delivery, value); err != nil {
		return nil, err
	}
	// explicit is the caller's ORIGINAL argument, empty or not — kept
	// separate from the resolved value below so a value rotation that
	// doesn't repeat its delivery choice can be told apart, in SQL, from
	// one that does. Losing that distinction would mean any rotation of
	// an existing env secret that omits --delivery silently reclassifies
	// it as file the moment this default flips — the app reading
	// os.Getenv would stop seeing the new value with no error anywhere,
	// exactly the kind of silent, persisted config loss #1671 already
	// burned this codebase on for a different field.
	explicit := delivery
	// Storage layer normalizes "" → "file" for a genuinely NEW row so the
	// column is always populated. #1604: env was the unspecified default
	// for a secret any same-container process can read via
	// /proc/<pid>/environ, AND the one Incus itself prints in cleartext
	// (`incus config show`) — a deployment fully migrated to envelope
	// encryption at rest could still leak every secret through this
	// path, because the two never interacted. file (0440 root:<tenant>
	// on tmpfs, absent from the Incus config entirely) closes both, and
	// is the default an operator gets without having to know to ask for
	// it. env remains fully supported and selectable via --delivery for
	// apps that only read os.Getenv.
	resolved := delivery
	if resolved == "" {
		resolved = DeliveryFile
	}

	// Broker-only is one-way. Without this check, the same SetSecret RPC
	// any secrets:write caller can issue would silently turn off the
	// "never delivered" guarantee a tracker connection (or any other
	// broker consumer) depends on. This is an operator-facing guard, not
	// a concurrency control — a race against a concurrent Set on the same
	// row falls back to ordinary last-write-wins, same as every other
	// field this method updates.
	if explicit != "" && explicit != DeliveryBroker {
		var existing string
		switch err := s.pool.QueryRow(ctx,
			`SELECT delivery FROM secrets WHERE username = $1 AND name = $2`,
			username, name).Scan(&existing); {
		case err == nil:
			if existing == DeliveryBroker {
				return nil, ErrBrokerModeImmutable
			}
		case errors.Is(err, pgx.ErrNoRows):
			// New row — nothing to protect.
		default:
			return nil, fmt.Errorf("check existing delivery mode: %w", err)
		}
	}

	nonce, ct, wrappedDEK, kekID, err := s.encryptForStorage(ctx, username, name, []byte(value))
	if err != nil {
		return nil, err
	}

	// INSERT ... ON CONFLICT DO UPDATE handles both create and
	// rotate in a single round-trip. The version bumps on every
	// rotation; the row's created_at stays as the original
	// (set-once-ever timestamp), updated_at moves to NOW().
	//
	// delivery's CASE: an unspecified ($8 = '') rotation preserves
	// whatever the row already had — only an EXPLICIT delivery argument
	// changes an existing row's mode. A brand-new row (the INSERT branch,
	// no conflict) always gets `resolved`, which is `explicit` when given
	// or file otherwise.
	const q = `
		INSERT INTO secrets (username, name, nonce, ciphertext, wrapped_dek, kek_id, delivery, version)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 1)
		ON CONFLICT (username, name)
		DO UPDATE SET
			nonce       = EXCLUDED.nonce,
			ciphertext  = EXCLUDED.ciphertext,
			wrapped_dek = EXCLUDED.wrapped_dek,
			kek_id      = EXCLUDED.kek_id,
			delivery    = CASE WHEN $8 = '' THEN secrets.delivery ELSE EXCLUDED.delivery END,
			version     = secrets.version + 1,
			updated_at  = NOW()
		RETURNING version, created_at, updated_at, delivery;
	`
	var version int32
	var createdAt, updatedAt time.Time
	var finalDelivery string
	if err := s.pool.QueryRow(ctx, q, username, name, nonce, ct, wrappedDEK, kekID, resolved, explicit).
		Scan(&version, &createdAt, &updatedAt, &finalDelivery); err != nil {
		return nil, fmt.Errorf("upsert secret: %w", err)
	}
	return &SecretMetadata{
		Username:  username,
		Name:      name,
		Version:   version,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
		Delivery:  finalDelivery,
	}, nil
}

// encryptForStorage picks the right encryption mode based on
// whether the Store has a KMSClient. Returns the row tuple to
// INSERT/UPDATE: (nonce, ciphertext, wrapped_dek_or_nil,
// kek_id_or_empty).
//
// Envelope path zeroes the DEK from memory before returning so
// the plaintext key doesn't outlive the function frame. The
// wrapped DEK is safe to hand back — it's encrypted under the
// KEK.
func (s *Store) encryptForStorage(ctx context.Context, username, name string, plaintext []byte) (nonce, ct, wrappedDEK []byte, kekID string, err error) {
	// #1630 — username's own KEK override if it has one, else the
	// shared/default s.kms (nil = legacy mode).
	kms, err := s.resolveEncryptKMS(username)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("resolve kms for %s: %w", username, err)
	}
	if kms == nil {
		// Legacy mode: master-key encrypt directly.
		nonce, ct, err = s.cipher.Encrypt(username, name, plaintext)
		if err != nil {
			return nil, nil, nil, "", fmt.Errorf("encrypt (legacy): %w", err)
		}
		return nonce, ct, nil, "", nil
	}

	// Envelope mode: fresh DEK, encrypt under it, wrap the DEK.
	dek, err := corecrypto.NewDEK()
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("generate DEK: %w", err)
	}
	defer corecrypto.ZeroBytes(dek)

	dekCipher, err := corecrypto.NewCipher(dek)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("build DEK cipher: %w", err)
	}
	nonce, ct, err = dekCipher.Encrypt(username, name, plaintext)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("encrypt (envelope): %w", err)
	}

	wrappedDEK, kekID, err = kms.Wrap(ctx, dek)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("KMS wrap: %w", err)
	}
	return nonce, ct, wrappedDEK, kekID, nil
}

// decryptFromStorage is the inverse — picks legacy vs envelope
// path based on whether wrapped_dek is populated. Zeros the DEK
// after use in the envelope branch.
//
// kms_id_check: if a row's kek_id doesn't match what the Store's
// KMSClient implementation expects (e.g. a row wrapped under
// "gcp-kms:..." reaching an InProcKMS-only daemon), the KMS
// returns an error from Unwrap — that's the signal a future
// daemon has been swapped to a different KMS without running the
// migration.
func (s *Store) decryptFromStorage(ctx context.Context, username, name string, nonce, ct, wrappedDEK []byte, kekID string) ([]byte, error) {
	// Legacy row: wrapped_dek IS NULL, kek_id IS NULL.
	if len(wrappedDEK) == 0 {
		if s.requireEnvelope {
			return nil, fmt.Errorf("secret %s/%s is legacy-encrypted but require_envelope=true (run `containarium secrets migrate-to-envelope` before retiring the master key)", username, name)
		}
		return s.cipher.Decrypt(username, name, nonce, ct)
	}
	// Envelope row. #1630 — resolved from the ROW's own kek_id, not
	// from any "current tenant override" state; see resolveDecryptKMS.
	kms, err := s.resolveDecryptKMS(kekID)
	if err != nil {
		return nil, fmt.Errorf("resolve kms for kek_id %q: %w", kekID, err)
	}
	if kms == nil {
		return nil, fmt.Errorf("secret %s/%s is envelope-encoded (kek_id=%q) but Store has no KMSClient configured", username, name, kekID)
	}
	dek, err := kms.Unwrap(ctx, wrappedDEK, kekID)
	if err != nil {
		return nil, fmt.Errorf("KMS unwrap: %w", err)
	}
	defer corecrypto.ZeroBytes(dek)
	dekCipher, err := corecrypto.NewCipher(dek)
	if err != nil {
		return nil, fmt.Errorf("build DEK cipher: %w", err)
	}
	return dekCipher.Decrypt(username, name, nonce, ct)
}

// resolveEncryptKMS picks the KMSClient a NEW Set for username should
// wrap under: username's per-tenant override if one is set (#1630),
// otherwise the shared/default s.kms (nil = legacy mode, unchanged
// behavior for every daemon that hasn't configured per-tenant keys).
func (s *Store) resolveEncryptKMS(username string) (corecrypto.KMSClient, error) {
	if s.tenantKMSFactory == nil {
		return s.kms, nil
	}
	s.tenantKEKMu.RLock()
	keyName, ok := s.tenantKEK[username]
	s.tenantKEKMu.RUnlock()
	if !ok {
		return s.kms, nil
	}
	return s.kekClient(keyName)
}

// resolveDecryptKMS picks the KMSClient that can unwrap a row whose
// kek_id is kekID. GCP rows are routed by the KEY NAME parsed out of
// kek_id, not by any "current tenant override" — every row decrypts
// under whatever key it actually says it's under, independent of
// whether SetTenantKMSKey/ClearTenantKMSKey has since moved that
