//go:build containarium_client

package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// osExit is os.Exit, indirected so tests can observe the exit code instead
// of killing the test binary.
var osExit = os.Exit

// movedServerCommands are every top-level server/operator command name that
