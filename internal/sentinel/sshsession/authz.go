package sshsession

import (
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"strings"
)

// Shared metadata keys survive sshpiperd's NextPlugin transition and are read
// by the session observer on both pipe start and close.
const (
	MetaUserID        = "containarium.ssh_authz.user_id"
	MetaAuthzDecision = "containarium.ssh_authz.decision"
	MetaDenyReason    = "containarium.ssh_authz.deny_reason"
	// MetaEnforced is "true" for every session ssh-authz-plugin admitted in
	// enforce mode, including one it routed without a daemon decision (a
	// certificate); empty in audit-only mode.
	MetaEnforced = "containarium.ssh_authz.enforced"
	// MetaTarget is the upstream host:port the enforcing ssh-authz-plugin
	// routed the connection to. Both the open and the close record carry it,
