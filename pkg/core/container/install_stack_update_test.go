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
