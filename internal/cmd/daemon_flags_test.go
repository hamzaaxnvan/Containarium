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
	}{
		{"bridge-dns-reconcile", "--bridge-dns-reconcile=false"},
		{"bridge-dns-create", "--bridge-dns-create"},
		{"dns-passthrough-host", "--dns-passthrough-host=api.example.com"},
		{"dns-passthrough-host", "--dns-passthrough-host="},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			if daemonCmd.Flags().Lookup(tc.name) != nil {
				t.Errorf("removed flag --%s is still registered", tc.name)
			}

			fs := pflag.NewFlagSet("daemon", pflag.ContinueOnError)
			fs.SetOutput(io.Discard)
			fs.AddFlagSet(daemonCmd.Flags())
			err := fs.Parse([]string{tc.arg})
			if err == nil || !strings.Contains(err.Error(), "unknown flag: --"+tc.name) {
				t.Errorf("parse %q: got %v, want unknown flag: --%s", tc.arg, err, tc.name)
			}
		})
