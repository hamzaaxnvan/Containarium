package server

import (
	"context"
	"log"

	"github.com/footprintai/containarium/pkg/core/zfscrypt"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Encryption state on container status (#1202), design resolved decision #3.
//
// ZFS allows a snapshot to be TAKEN while the key is unloaded and refuses
// only to read one back. That asymmetry is deliberate — blocking snapshot
// creation on key-custody reachability would let a transient outage suppress
// the backup window — but it means a tenant can accumulate snapshots that
// cannot be restored, and nothing would say so until someone tried.
//
// So the condition is surfaced on status, before anyone trips over it.

// encryptionStateFor reports whether a container is encrypted and whether its
// key is currently loaded.
//
// Never guesses. A state that could not be read is UNSPECIFIED, not NONE:
// an operator reading "not encrypted" would conclude there is nothing to
// investigate, which is the opposite of what an unreadable state means.
