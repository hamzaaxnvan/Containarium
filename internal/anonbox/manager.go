// Package anonbox is the daemon side of the `ssh new.<cloud-domain>` door
// (docs/architecture/ssh-new-anonymous-box.md, #2197): it owns the
// SSH-key-fingerprint → VM mapping for anonymous boxes.
//
// An anonymous box is an ordinary Containarium box with three differences
// the Manager enforces on every create: it is an Incus VM (never a
// shared-kernel container), it is born with a TTL, and its NIC carries an
// egress ACL that drops everything but DNS, HTTP and HTTPS. Its state —
// which fingerprint it belongs to, when it was created, its claim token id
// — lives in the box's own labels, the same place TTL and auto-sleep state
// already live, so there is no second store to drift from Incus.
//
// The Manager is pure control logic over two narrow, fakeable seams (Boxes,
// ACLs); wiring it to the real box backend and Incus client, and exposing
// it as AnonymousBoxService, is the follow-up PR on the same issue.
// Rate limits, the global cap, the kill switch and bans are #2200; the
// claim token itself is #2199 (the ClaimURL hook is its seam).
package anonbox

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/lxc/incus/v7/shared/api"
	"golang.org/x/crypto/ssh"

	"github.com/footprintai/containarium/internal/sandbox/ratelimit"
	"github.com/footprintai/containarium/pkg/core/box"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Label keys stamped on an anonymous box. They ride the normal label
// mechanism (user.containarium.label.<key> in Incus) so BoxStatus.Labels
// carries them back on List/Get without a new read path.
const (
	LabelFingerprint  = "anon.fingerprint"    // "SHA256:…" — the lookup key; one live box per value
	LabelFPHash       = "anon.fp_hash"        // sha256 hex of the fingerprint (what events carry)
	LabelCreatedAt    = "anon.created_at"     // RFC3339
	LabelSourceIP     = "anon.source_ip"      // retained for abuse handling; dies with the box
	LabelClaimTokenID = "anon.claim_token_id" // random 16 B hex, embedded in the claim token (#2199)
	LabelClaimedAt    = "anon.claimed_at"     // empty until claimed; the CAS target of #2199
	LabelPublicKey    = "anon.public_key"     // the claiming key's authorized_keys line, kept on claim
)

// In-guest paths the Manager writes.
const (
	ClaimURLPath = "/etc/containarium/claim-url"
	BannerPath   = "/etc/update-motd.d/50-containarium-anon"
)

// Limits are the fixed per-box caps and the birth TTL. Fixed, not
// per-request: an anonymous caller does not get to pick.
type Limits struct {
	CPU    string
	Memory string
	Disk   string
	TTL    time.Duration

	// Guardrails (#2200). MaxBoxes caps live UNCLAIMED anonymous boxes on
	// this daemon; the rate limits apply to creates only (a reconnect is
	// free), per key fingerprint and per source IP, as token buckets.
	// 0 = that limit is off.
	MaxBoxes        int
	PerKeyPerMinute float64
	PerKeyBurst     int
	PerIPPerMinute  float64
	PerIPBurst      int
}

// DefaultLimits is the owner's decision on #2204 (2026-10-01): 2 vCPU /
// 4 GB / 20 GB / 4 h; cap 20; per key 1 create per 10 min (burst 2); per
// source IP 6 per 10 min (burst 6).
func DefaultLimits() Limits {
	return Limits{
		CPU: "2", Memory: "4GB", Disk: "20GB", TTL: 4 * time.Hour,
		MaxBoxes: 20, PerKeyPerMinute: 0.1, PerKeyBurst: 2, PerIPPerMinute: 0.6, PerIPBurst: 6,
	}
}

// Rejections a caller can cause (#2200). The RPC layer maps them:
// DoorClosedError → FailedPrecondition with its message, ErrBanned →
// PermissionDenied, ErrRateLimited / ErrAtCapacity → ResourceExhausted.
var (
	ErrBanned      = errors.New("anonbox: this key is banned from the anonymous door")
	ErrRateLimited = errors.New("anonbox: slow down — too many new boxes from this key or address; try again in a few minutes")
	ErrAtCapacity  = errors.New("anonbox: we're full right now — sign up for a guaranteed box, or try again later")
)

// DoorClosedError carries the operator's message for a closed door.
type DoorClosedError struct{ Message string }

func (e DoorClosedError) Error() string {
	if e.Message == "" {
		return "anonbox: the anonymous door is closed"
	}
	return "anonbox: " + e.Message
}

// Config is everything the Manager needs beyond its two backends.
type Config struct {
	Limits Limits

	// NICDevice and Bridge name the instance-local NIC the egress ACL is
	// attached to (see incus.EnsureNICDevice for why it must be
	// instance-local). Typically "eth0" on the daemon's bridge.
	NICDevice string
	Bridge    string

	// ClaimSecret returns the per-box HMAC secret for claim tokens —
	// TokenManager.DeriveSharedSecret("anon-claim", boxName) in the daemon.
	// nil = no token is minted, no claim-url file is written, and Claim
	// refuses; the banner still shows the `containarium claim` hint.
	ClaimSecret func(boxName string) string
	// ClaimURLBase is prefixed to the token in the guest's claim-url file
	// as "<base>?token=<token>" (e.g. https://<cloud-domain>/claim). Empty
	// = the file holds the bare token (decision on #2199: the CLI prints
	// it with a note).
	ClaimURLBase string

	// DoorStatePath persists the kill switch and bans (#2200); "" keeps
	// them in memory only. A malformed file fails closed — see DoorStore.
	DoorStatePath string

	// Funnel records every step of a key's journey (#2201); nil = none.
	Funnel Funnel

	// Logf receives operational warnings (a wall that could not be
	// delivered, #2202); nil = the standard logger.
	Logf func(format string, args ...any)

	// Reminder delivers the opt-in expiry reminder (#2206); nil = never sent.
	Reminder ReminderSender

	// Now is the clock; nil = time.Now.
	Now func() time.Time
}

// Boxes is the slice of box.BoxBackend (+ ExecCapable + TTLCapable) the
// Manager uses. The LXC backend satisfies it directly.
type Boxes interface {
	Create(ctx context.Context, spec box.BoxSpec) (*box.BoxStatus, error)
	Delete(ctx context.Context, ref box.BoxRef, force bool) error
	List(ctx context.Context) ([]box.BoxStatus, error)
	SetTTL(ctx context.Context, ref box.BoxRef, expiresAt *time.Time) error
	Exec(ctx context.Context, ref box.BoxRef, cmd []string) (stdout, stderr string, err error)
	WriteFile(ctx context.Context, ref box.BoxRef, path string, content []byte, mode string) error
	// Claim (#2199) — all addressed by boxRefFor(name), see Claim.
	SetMeta(ctx context.Context, ref box.BoxRef, meta map[string]string) error
	SetAuthorizedKeys(ctx context.Context, ref box.BoxRef, keys []string) error
	SetOwner(ctx context.Context, ref box.BoxRef, tenant string) error
}

// ACLs is the Incus network-ACL + NIC-device slice. incus.Backend
// satisfies it; the core-infra guard uses the same five calls.
type ACLs interface {
	GetNetworkACL(name string) (*api.NetworkACL, error)
	CreateNetworkACL(config incus.ACLConfig) error
	UpdateNetworkACL(name string, config incus.ACLConfig) error
	EnsureNICDevice(containerName string, want incus.NICDevice) error
	SetDeviceConfig(containerName, deviceName string, keys map[string]string) error
}

// InvalidRequestError marks a failure that is the caller's fault (a key
// that does not parse, a fingerprint that does not match it) so the RPC
// layer can answer InvalidArgument rather than Internal.
type InvalidRequestError struct{ Err error }

func (e InvalidRequestError) Error() string { return e.Err.Error() }
func (e InvalidRequestError) Unwrap() error { return e.Err }

// EnsureRequest is what the door knows about a connecting key.
type EnsureRequest struct {
	Fingerprint string // "SHA256:<base64>", as ssh.FingerprintSHA256; optional, verified against PublicKey when set
	PublicKey   string // one authorized_keys line
	SourceIP    string
}

// EnsureResult is the upstream the door should pipe the session to.
type EnsureResult struct {
	BoxName         string
	SSHHost         string
	SSHPort         int
	SSHUser         string
	TTLExpiresAt    time.Time
	Reused          bool // an existing live box for this key
	PreviousExpired bool // a box for this key existed earlier in this daemon's lifetime and is gone
}

// Manager resolves or creates the anonymous box for a key.
type Manager struct {
	boxes Boxes
	acls  ACLs
	cfg   Config

	// seen remembers fingerprints this process has created a box for, so a
	// reconnect after the sweeper reaped the box can be told apart from a
	// first visit. Best-effort by design: it does not survive a daemon
	// restart, and a wrong "false" only costs one banner line.
	mu    sync.Mutex
	seen  map[string]time.Time
	known map[string]knownBox // Observe's previous snapshot (#2201)

	// claimMu serializes Claim's compare-and-set on claimed_at: the box
	// backend has no atomic label update, so the daemon is the lock.
	claimMu sync.Mutex

	// Guardrails (#2200).
	door       *DoorStore
	keyLimiter *ratelimit.Limiter
	ipLimiter  *ratelimit.Limiter
}

// New returns a Manager over the given backends.
func New(boxes Boxes, acls ACLs, cfg Config) *Manager {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	if cfg.Limits == (Limits{}) {
		cfg.Limits = DefaultLimits()
	}
	if cfg.NICDevice == "" {
		cfg.NICDevice = "eth0"
	}
	return &Manager{
		boxes: boxes, acls: acls, cfg: cfg, seen: map[string]time.Time{},
		door:       NewDoorStore(cfg.DoorStatePath),
		keyLimiter: ratelimit.New(cfg.Limits.PerKeyPerMinute/60, cfg.Limits.PerKeyBurst),
