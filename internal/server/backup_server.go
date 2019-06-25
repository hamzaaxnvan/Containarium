package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	appconfig "github.com/footprintai/containarium/internal/config"
	secretsstore "github.com/footprintai/containarium/internal/secrets"
	"github.com/footprintai/containarium/pkg/core/backup"
	corecrypto "github.com/footprintai/containarium/pkg/core/secrets"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// defaultBackupDir is where dumps (for LOCAL) and the JSON index (for all
// destinations) live on the daemon host. Overridable via the
// CONTAINARIUM_BACKUP_DIR env var. Kept off the container data disks so a
// backup never shares a failure domain with the database it describes.
const defaultBackupDir = "/var/lib/containarium/backups"

// BackupServer implements the gRPC BackupService. It is orchestration over
// the existing ContainerServer: CreateBackup runs pg_dump inside the
// tenant's container (via the container manager's Exec/ReadFile), then
// stores the dump off-host. Lives in package server to reuse the wired
// container manager.
type BackupServer struct {
	pb.UnimplementedBackupServiceServer
	containers *ContainerServer
	mgr        *backup.Manager
	verifierMu sync.Mutex
	verifiers  map[string]bool
}

// acquireVerifier reserves one managed verifier per tenant. The release must
// run after container cleanup, including when provisioning or verification fails.
func (s *BackupServer) acquireVerifier(tenant string) (release func(), ok bool) {
	s.verifierMu.Lock()
	defer s.verifierMu.Unlock()
	if s.verifiers[tenant] {
		return nil, false
	}
	if s.verifiers == nil {
		s.verifiers = make(map[string]bool)
	}
	s.verifiers[tenant] = true
	return func() {
		s.verifierMu.Lock()
		defer s.verifierMu.Unlock()
		delete(s.verifiers, tenant)
	}, true
}

// Manager exposes the backup core for wiring into other subsystems that
// need read access to the durable backup index — currently the metrics
// export collector's backup-health series (#2294), via
// ContainerServer.SetBackupManager. Nothing else should need this: it is
// a seam for cross-cutting observability, not a general escape hatch
// around BackupServer's own RPC surface.
func (s *BackupServer) Manager() *backup.Manager {
	return s.mgr
}

// envBackupKMSKeyName names the KMS key the daemon wraps per-backup
// identities under (#2402): a full CryptoKey resource name, reached via
// the daemon's existing CONTAINARIUM_KMS_BACKEND=gcp configuration. Empty
// means no wrapper — the #1831 / #1836 behaviour, byte for byte.
const envBackupKMSKeyName = "CONTAINARIUM_BACKUP_KMS_KEY_NAME"

// NewBackupServer wires the backup service to the container manager. A GCS
// uploader is constructed best-effort: if `gcloud` is absent the daemon
// still serves LOCAL backups and rejects GCS requests with a clear error,
// rather than failing to start.
//
// The managed-backup key wrapper (#2402) is NOT best-effort: when
// CONTAINARIUM_BACKUP_KMS_KEY_NAME is set but no wrapper can be built —
// the KMS backend is not gcp, the credentials are missing, the name is
// not a CryptoKey — the daemon fails to start. An operator who configured
// managed backups and silently got legacy ones would learn about it at
// restore time, which is the one moment that must not hold surprises.
func NewBackupServer(containers *ContainerServer) (*BackupServer, error) {
	dir := os.Getenv("CONTAINARIUM_BACKUP_DIR")
	if dir == "" {
		dir = defaultBackupDir
	}

	var uploader backup.Uploader
	if u, err := backup.NewGcloudUploader(); err != nil {
		log.Printf("[backup] GCS uploader unavailable (%v); LOCAL backups only", err)
	} else {
		uploader = u
	}

	wrapper, err := loadBackupWrapper()
	if err != nil {
		return nil, err
	}
	opts, err := boxBackupOptionsFromEnv(containers.manager)
	if err != nil {
		return nil, err
	}
	if wrapper != nil {
		opts = append(opts, backup.WithWrapper(wrapper))
		containers.SetManagedBackupKeyConfigured(true)
		log.Printf("[backup] managed key mode enabled (wrapping per-backup identities under %s)", os.Getenv(envBackupKMSKeyName))
	}

	mgr := backup.NewManager(containers.manager, uploader, dir, opts...)
	// A daemon killed mid whole-box backup leaves its spill file behind.
	if n, err := mgr.SweepStaleBoxSpills(); err != nil {
		log.Printf("[backup] sweeping stale whole-box spill files: %v", err)
	} else if n > 0 {
		log.Printf("[backup] removed %d stale whole-box spill file(s) at start", n)
	}
	return &BackupServer{
		containers: containers,
		mgr:        mgr,
	}, nil
}

// Whole-box backup settings (#2801).
const (
	// envBackupBoxMaxBytes overrides the 256 GiB cap on a stored BOX
	// backup, in bytes.
	envBackupBoxMaxBytes = "CONTAINARIUM_BACKUP_BOX_MAX_BYTES"
	// envIncusBackupsDir is where Incus stages an export before streaming
	// it (Incus 6.0 LTS has no direct_backup). The free-space pre-flight
	// counts a copy of the box there.
	envIncusBackupsDir = "CONTAINARIUM_INCUS_BACKUPS_DIR"
	// defaultIncusBackupsDir is that directory on a default Incus install.
	defaultIncusBackupsDir = "/var/lib/incus/backups"
)

// boxBackupOptionsFromEnv wires whole-box backup into the backup core: the
// container manager as the export, the Incus staging dir for the
// pre-flight, and the cap. A cap that does not parse fails startup, like a
// bad KMS key name: an operator who set one and silently got the default
// would find out when a backup is refused, or when the disk fills.
func boxBackupOptionsFromEnv(box backup.BoxOps) ([]backup.Option, error) {
	var opts []backup.Option
	if box != nil {
		opts = append(opts, backup.WithBoxOps(box))
	}
	dir := strings.TrimSpace(os.Getenv(envIncusBackupsDir))
	if dir == "" {
		dir = defaultIncusBackupsDir
	}
	opts = append(opts, backup.WithBoxStagingDirs(dir))
	if raw := strings.TrimSpace(os.Getenv(envBackupBoxMaxBytes)); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("%s=%q: want a positive number of bytes", envBackupBoxMaxBytes, raw)
		}
		opts = append(opts, backup.WithMaxBoxBytes(n))
	}
	return opts, nil
}

// loadBackupWrapper builds the key wrapper from CONTAINARIUM_BACKUP_KMS_
// KEY_NAME through the same resource-name keyed factory the secrets
// store uses for per-tenant KEKs, so the backup key rides the daemon's
// existing KMS auth (token / token file / endpoint). Returns (nil, nil)
// when the variable is empty.
func loadBackupWrapper() (corecrypto.KMSClient, error) {
	keyName := strings.TrimSpace(os.Getenv(envBackupKMSKeyName))
	if keyName == "" {
		return nil, nil
	}
	factory, err := secretsstore.LoadTenantKMSFactory()
	if err != nil {
		return nil, fmt.Errorf("%s is set but the KMS backend could not be loaded: %w", envBackupKMSKeyName, err)
	}
	if factory == nil {
		return nil, fmt.Errorf("%s is set but CONTAINARIUM_KMS_BACKEND is not \"gcp\" (got %q): managed backups need a resource-name keyed KMS backend",
			envBackupKMSKeyName, os.Getenv(appconfig.EnvKMSBackend))
	}
	wrapper, err := factory(keyName)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", envBackupKMSKeyName, err)
	}
	return wrapper, nil
}

// CreateBackup dumps a tenant's database and stores it off-host.
func (s *BackupServer) CreateBackup(ctx context.Context, req *pb.CreateBackupRequest) (*pb.CreateBackupResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeBackupsWrite); err != nil {
		return nil, err
	}
	if req.Username == "" {
		return nil, status.Error(codes.InvalidArgument, "username is required")
	}
	if err := auth.AuthorizeTenant(ctx, req.Username); err != nil {
		return nil, err
	}

	scope, err := scopeFromProto(req.Scope)
	if err != nil {
		return nil, err
	}
	if scope == pb.BackupScope_BACKUP_SCOPE_BOX {
		return s.createBoxBackup(ctx, req)
	}

	dest, err := destFromProto(req.Destination)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// #1836: an explicit request recipient always wins; otherwise fall
	// back to the tenant's own self-registered CONTAINARIUM_BACKUP_AGE_RECIPIENT
	// secret, so a scheduled backup with no operator present still
	// encrypts. Neither a standalone daemon (no secrets store) nor a
	// tenant who never registered one is an error — both mean plaintext,
	// today's unchanged default (or, on a daemon with a key wrapper,
	// MANAGED instead of BOTH — #2402).
	ageRecipient, err := resolveAgeRecipient(ctx, backupSecretsReader(s.containers), req.Username, req.AgeRecipient)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	// #2402: a key mode this daemon cannot satisfy is refused here, with
	// a code that says why, before any container is touched. The core
	// re-checks the same rules; this is where they become gRPC codes.
	keyMode, err := keyModeFromProto(req.KeyMode, s.mgr.HasWrapper(), ageRecipient != "")
	if err != nil {
