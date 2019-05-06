//go:build incus

package incus_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/testsupport/incusenv"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/zfscrypt"
	"github.com/footprintai/containarium/pkg/core/zfskey"
)

// #1338 AC3, and the only claim in this package that a fake cannot make.
//
// The unit tests pin what reaches Incus. Whether Incus will actually build an
// instance inside a pool sourced at an ENCRYPTED dataset — rather than
// refusing it the way it refuses a pre-existing instance dataset (#1335) — is
// a property Incus and ZFS compute between them. #1341 asserts the encryption
// that results; this asserts the pool works at all, so a failure here points
// at the pool operations rather than at the hook wiring above them.
//
// External test package (incus_test) on purpose: it uses the daemon's public
// surface, which is what #1340's hook will hold.

func TestIntegrationIncus_PoolSourcedAtAnEncryptedDataset(t *testing.T) {
	client := incusenv.Require(t)

	zfs := zfscrypt.NewManager(zfscrypt.ExecRunner{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

