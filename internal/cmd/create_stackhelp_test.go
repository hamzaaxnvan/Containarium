//go:build !windows

package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/stacks"
)

// TestStackFlagUsageShowsNames guards the --stack usage string against the two
// ways it was previously wrong: a hard-coded list that drifted from the catalog
// (eight ids listed, fifteen shipped) and ids shown without their human name, so
