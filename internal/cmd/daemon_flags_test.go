//go:build !windows && !containarium_client

package cmd

import (
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// #2735 (slice D of #2263): the compatibility release has shipped. The
// removed bridge DNS flags must now fail parsing rather than remain no-ops.
func TestBridgeDNSFlagsRemoved(t *testing.T) {
	for _, tc := range []struct {
		name string
		arg  string
