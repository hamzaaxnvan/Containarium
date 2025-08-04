package sshauthz

import (
	"context"
	"errors"
	"log"
	"time"
)

// ErrKeyHeldByOtherAccount is returned by Revoke when the revocation would
// take effect but another account still holds the same public key in its own
// authorized_keys (#3222). It deliberately carries no account name.
var ErrKeyHeldByOtherAccount = errors.New("ssh key is still held by another account")

// ErrHolderUnknown is returned when an account's authorized_keys could not be
// read, so it cannot be ruled out as a holder and the revoke fails closed
// (#3241). It reaches API callers, possibly of a different tenant, so it
// names neither the account nor the filesystem state; both are in the
// daemon log.
var ErrHolderUnknown = errors.New("cannot rule out another account as a holder: an account's authorized_keys could not be read; check the daemon log")

// ErrHomeRootUnreadable is returned when the home root itself could not be
// scanned, so no account can be ruled out as a holder and the revoke fails
// closed (#3276). Like ErrHolderUnknown it reaches API callers, so it carries
// neither the path nor the os error; both are in the daemon log.
var ErrHomeRootUnreadable = errors.New("cannot rule out another account as a holder: the account home directory could not be read; check the daemon log")

// HolderLookup reports whether any account other than excludeUsername holds
// the key with the given fingerprint.
type HolderLookup interface {
	OtherAccountHolds(ctx context.Context, fingerprint, excludeUsername string) (bool, error)
}
