// Package sshsession implements containarium#1980: a structured session
// lifecycle record for SSH connections accepted by the sentinel's
// sshpiperd. sshpiperd is the only place a client's key or certificate is
// verified (containers run no sshd of their own), but today an accepted
// connection produces no record at all — only the failtoban plugin's
// auth-failure line exists. This package plugs into sshpiperd's own
// plugin chain (see plugin.go) to emit an "open" record when a pipe is
// established and a "close" record when it ends, correlated by
// session_id. Authorization decisions can also be carried on a "denied"
// record without changing failtoban's separate failure-path logging.
package sshsession

import (
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// SessionPhase distinguishes accepted-session lifecycle and authorization
// denial records. Typed per this repo's strong-typing convention (CLAUDE.md) —
// not a bare string with a comment listing the allowed values.
type SessionPhase string

const (
	SessionPhaseOpen   SessionPhase = "open"
	SessionPhaseClose  SessionPhase = "close"
	SessionPhaseDenied SessionPhase = "denied"
	// SessionPhaseAuthFailed summarises the failed authentications from one
	// source IP in one ban window; SessionPhaseBanned records a ban (#2419).
	// Neither belongs to a session: the ban plugin writes them, with no
	// login, target or credential.
	SessionPhaseAuthFailed SessionPhase = "auth_failed"
	SessionPhaseBanned     SessionPhase = "banned"
)

// AuthMethod is how the client's credential was presented.
type AuthMethod string

const (
	AuthMethodCertificate AuthMethod = "certificate"
	AuthMethodPublicKey   AuthMethod = "publickey"
	// AuthMethodUnknown is recorded when the offered key could not be
	// parsed at all (see ExtractCredential) — it is never used to block
	// a connection, only to mark that this record's credential fields
	// could not be populated.
	AuthMethodUnknown AuthMethod = "unknown"
)

// CloseReason classifies why a "close" record was emitted.
type CloseReason string

const (
