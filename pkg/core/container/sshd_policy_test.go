package container

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus/incustest"
	"github.com/footprintai/containarium/pkg/core/ospkg"
	"github.com/footprintai/containarium/pkg/core/ostype"
	"github.com/footprintai/containarium/pkg/core/sshdpolicy"
)

// Every box — create and bake alike, both families — gets the managed
// key-only sshd drop-in, written before sshd is (re)started so the
// running daemon serves it (#2424).
func TestInstallPackages_WritesKeyOnlySSHDDropIn(t *testing.T) {
	for _, family := range []ostype.OSFamily{ostype.Debian, ostype.RHEL} {
		t.Run(string(family), func(t *testing.T) {
			b := &provisionRecorder{}
			if err := NewWithBackend(b).installPackages("box-container", false, "", nil, "", family); err != nil {
				t.Fatalf("installPackages: %v", err)
			}
			got, ok := b.writes[sshdpolicy.DropInPath]
			if !ok {
				t.Fatalf("drop-in not written to %s; writes: %v", sshdpolicy.DropInPath, keys(b.writes))
			}
			if !bytes.Equal(got, sshdpolicy.DropInContent()) {
				t.Errorf("drop-in content differs from sshdpolicy.DropInContent()")
			}
