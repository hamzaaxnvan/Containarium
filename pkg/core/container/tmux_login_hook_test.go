package container

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/ostype"
)

// provisionRecorder fakes the calls installPackages makes: it records every
// Exec argv and every WriteFile, and can fail WriteFile on demand.
type provisionRecorder struct {
	incus.Backend
	execs        [][]string
	writes       map[string][]byte
	modes        map[string]string
	writeFileErr error
	failPath     string // when set, only a WriteFile to this path returns writeFileErr
	moshExecErr  error  // returned by the separate mosh install script (RHEL/EPEL)
}

func (b *provisionRecorder) ExecWithExitCode(_ string, _ []string) (string, string, int, error) {
	return "", "", 1, nil // no cloud-init: skip the wait
}

func (b *provisionRecorder) Exec(_ string, command []string) error {
	b.execs = append(b.execs, command)
	if isMoshInstall(command) {
		return b.moshExecErr
	}
	return nil
}

