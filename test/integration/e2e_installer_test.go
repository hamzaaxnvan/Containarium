package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2EInstallerQuickstart is the installer-level (Layer 3) e2e for
// `install.sh --quickstart`. It provisions a BARE Ubuntu 24.04 VM (not the
// containarium terraform module, which pre-installs the daemon), copies the
// tree's hacks/install.sh onto it, and runs the fresh-VM bootstrap:
//
//	sudo bash install.sh --quickstart alice --stack nodejs
//
// then asserts the daemon installed and the first box actually came up. This
// exercises the keyless path — the headline "skip bring-your-own-ssh-key"
// behavior — so it needs no key material and no SSH-into-the-box.
//
// Gated like the other GCP e2e tests: skips in -short and without GCP_PROJECT.
//
//	GCP_PROJECT=my-project GCP_ZONE=asia-east1-a \
//	  go test -v -run TestE2EInstallerQuickstart -timeout 40m ./test/integration/
