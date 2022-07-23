package cmd

import (
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// `containarium info` prints one line per storage pool and flags the ones
