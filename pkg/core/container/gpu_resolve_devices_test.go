package container

import (
	"fmt"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
)

// fakePCIResolver maps index inputs to canned PCI addresses; PCI-shaped inputs
// pass through unchanged, mirroring the real ResolveGPUInputToPCI contract.
func fakePCIResolver(byIndex map[string]string) func(string) (string, error) {
	return func(in string) (string, error) {
		if strings.Contains(in, ":") {
			return in, nil
		}
		if pci, ok := byIndex[in]; ok {
			return pci, nil
