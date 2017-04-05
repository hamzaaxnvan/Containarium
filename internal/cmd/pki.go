package cmd

import (
	"fmt"
	"os"

	"github.com/footprintai/containarium/pkg/core/pki"
	"github.com/spf13/cobra"
)

// pki is the Phase 0.5 peer-CA management command — distinct from
// the older `cert` command which manages the daemon's gRPC mTLS
