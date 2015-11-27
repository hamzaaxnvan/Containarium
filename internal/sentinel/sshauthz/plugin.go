package sshauthz

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/hostport"
	"github.com/footprintai/containarium/internal/sentinel/sshsession"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/tg123/sshpiper/libplugin"
	"golang.org/x/crypto/ssh"
)

const (
	DefaultGrace   = 5 * time.Minute
	DefaultTimeout = 3 * time.Second
	// MaxApprovalWait caps device-approval polling from the first PENDING.
	MaxApprovalWait = 60 * time.Second
)

type Authorizer interface {
	Authorize(context.Context, string, *pb.AuthorizeSSHRequest) (*pb.AuthorizeSSHResponse, error)
}

// BackendResolver must use the same login selection rule as KeyStore.Apply.
// Errors differ from unknown logins so a broken local bridge is audited.
type BackendResolver interface {
	ResolveBackend(context.Context, string) (backend string, known bool, err error)
}

// RoutingReadiness is optionally implemented by a BackendResolver whose
// "unknown login" answer is authoritative only once the keystore has synced
// every healthy backend (the same signal the session shipper uses). Enforce
// mode refuses an unknown login either way; the signal tells the log apart:
// a login no backend lists, or a keystore not yet synced after a restart.
type RoutingReadiness interface {
	RoutingReady(context.Context) bool
}

// errPublicKeyOnly refuses every non-publickey method. sshpiperd's chain
// cursor must stay on this plugin, so no other method can carry a
// connection past it to yaml's static authorized_keys.
var errPublicKeyOnly = errors.New("ssh authz: publickey is the only accepted method")

type Config struct {
	Authorizer Authorizer
	Resolver   BackendResolver
	Cache      *Cache
	Timeout    time.Duration
	SentinelID string
	Logf       func(string, ...any)
	// Enforce turns a DENY into an error from PublicKeyCallback: sshpiperd
	// keeps the chain on this plugin, so neither ssh-session-plugin nor yaml
	// is consulted for that key. An unknown login is refused the same way.
	// Every other offer is routed by Router and never deferred (see pass).
	// False is audit-only: every decision defers.
	Enforce bool
	// Router is required in enforce mode.
	Router *Router
	// Recorder receives the SessionPhaseDenied record of an enforced denial
	// and, in enforce mode, the open and close records of routed sessions.
	// No other plugin sees those credentials. Nil drops the records.
	Recorder sshsession.Recorder
	Target   *sshsession.TargetResolver // routed target on session records
	Now      func() time.Time           // nil uses time.Now().UTC()
	// Sleep replaces the device-approval poll wait (tests). Nil waits for
	// the interval or until the poll is cancelled, whichever comes first.
	Sleep func(time.Duration)
}

type Plugin struct {
	cfg     Config
	session *sshsession.Plugin

	mu      sync.Mutex
	pending map[string]pendingConn       // routed, pipe not started yet
	open    map[string]map[string]string // pipe started, not closed yet
	// approvals holds the device approval of the key a connection offered
	// last, until keyboard-interactive consumes it (approval.go).
	approvals map[string]pendingApproval
