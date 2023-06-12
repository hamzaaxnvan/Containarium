package sshauthz

import (
	"sort"
	"sync"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// ManagedIdentities records, for each managed AuthorizeSSH ALLOW, which
// control-plane user id the cloud named for a (credential, local login)
// pair (#2602). A managed TERMINATE_USER_SESSIONS assignment carries that
// control-plane id, not a host-local username; the terminate handler
// resolves it to local logins through this record.
//
// Only ALLOW decisions are recorded: a denial's user id names who tried,
// not whose login it is, and mapping it would let one user's deactivation
// end another user's sessions. A later ALLOW for the same pair replaces the
// earlier owner. The record is in memory: after a daemon restart a user is
// mappable again from their next login.
type ManagedIdentities struct {
	mu     sync.RWMutex
	owners map[identityKey]string // (credential, login) -> control-plane user id
}

type identityKey struct {
	credential string // public-key fingerprint, or "cert:<ca fingerprint>:<key id>"
	login      string
}

// NewManagedIdentities returns an empty record.
func NewManagedIdentities() *ManagedIdentities {
