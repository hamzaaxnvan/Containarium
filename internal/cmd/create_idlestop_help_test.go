//go:build !windows

package cmd

import (
	"strings"
	"testing"
)

// #2759: "Empty = no auto-sleep" is wrong on the hosted platform, where an org
// billed per seat defaults to a 30-minute idle-stop. The flag's help must say
// both defaults, so a user reading it is not surprised either way.
