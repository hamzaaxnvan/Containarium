package container

import (
	"bytes"
	"errors"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// installStackBackend records every Exec. Detection probes (powershell,
// redhat-release) fail so the box reads as Debian family.
type installStackBackend struct {
	incus.Backend
	execs [][]string
	// installErr, when set, fails the package-install Exec (apt-get install).
	installErr error
	config     map[string]string // SetConfig writes, by key
	// setConfigErrs are returned by successive SetConfig calls (nil = succeed).
	setConfigErrs  []error
	setConfigCalls int
}

func (b *installStackBackend) SetConfig(_, key, value string) error {
	b.setConfigCalls++
	if len(b.setConfigErrs) > 0 {
		err := b.setConfigErrs[0]
		b.setConfigErrs = b.setConfigErrs[1:]
		if err != nil {
			return err
		}
	}
	if b.config == nil {
		b.config = map[string]string{}
	}
	b.config[key] = value
	return nil
}

func (b *installStackBackend) GetContainer(name string) (*incus.ContainerInfo, error) {
	return &incus.ContainerInfo{Name: name, State: "Running"}, nil
}

func (b *installStackBackend) Exec(_ string, cmd []string) error {
	b.execs = append(b.execs, cmd)
	if b.installErr != nil && len(cmd) > 1 && cmd[0] == "apt-get" && cmd[1] == "install" {
		return b.installErr
	}
	if cmd[0] == "powershell" || (cmd[0] == "test" && len(cmd) > 2 && strings.Contains(cmd[2], "redhat-release")) {
		return errors.New("not this family")
	}
	return nil
}

func (b *installStackBackend) ran(prefix ...string) bool {
	for _, c := range b.execs {
		if len(c) >= len(prefix) && strings.Join(c[:len(prefix)], " ") == strings.Join(prefix, " ") {
			return true
		}
	}
	return false
}

// #2617: the startup base-script reconcile ran a package-index update in
// every running container although the script installs nothing. A base
// script with no packages must skip it; a dev stack still updates first.
func TestInstallStack_PackageIndexUpdate(t *testing.T) {
	t.Run("base script with no packages does not update the index", func(t *testing.T) {
		b := &installStackBackend{}
		if err := NewWithBackend(b).InstallStack("alice", "ntp"); err != nil {
			t.Fatalf("InstallStack: %v", err)
		}
		if b.ran("apt-get", "update") {
			t.Error("apt-get update ran for a base script that installs no packages")
		}
		if !b.ran("bash", "-c", "timedatectl set-timezone Asia/Taipei") {
			t.Errorf("the script's own pre-install step did not run: %v", b.execs)
		}
	})
	t.Run("dev stack still updates the index before installing", func(t *testing.T) {
		b := &installStackBackend{}
		if err := NewWithBackend(b).InstallStack("alice", "nodejs"); err != nil {
			t.Fatalf("InstallStack: %v", err)
		}
		if !b.ran("apt-get", "update") {
			t.Error("a stack with packages must update the index first")
		}
