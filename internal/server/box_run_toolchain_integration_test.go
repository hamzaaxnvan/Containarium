//go:build incus

package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/testsupport/incusenv"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/toolchain"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestIntegrationIncus_ManagedToolchainVolume(t *testing.T) {
	client := incusenv.Require(t)
	// A direct child of /tmp is traversable by an unprivileged container's
	// mapped root; testing.T.TempDir's private parent is deliberately not.
	root, err := os.MkdirTemp("", "ctn-toolchains-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"v1", "v2"} {
		bin := filepath.Join(root, version, "bin")
		if err := os.MkdirAll(bin, 0755); err != nil {
			t.Fatal(err)
		}
		for _, tool := range toolchain.Tools {
			if err := os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\necho "+version+"\n"), 0755); err != nil {
				t.Fatal(err)
			}
		}
	}
	activate := func(version string) {
		t.Helper()
		if err := os.Symlink(version, filepath.Join(root, "next")); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(root, "next"), filepath.Join(root, "current")); err != nil {
			t.Fatal(err)
		}
	}
	activate("v1")
	for _, lazy := range []bool{false, true} {
		t.Run(fmt.Sprintf("lazy=%v", lazy), func(t *testing.T) {
			name := fmt.Sprintf("toolchains-%d-%v", os.Getpid(), lazy)
			cfg := incus.ContainerConfig{Name: name, Image: incusenv.BoxImage(), Disk: &incus.DiskDevice{Path: "/", Pool: client.StoragePool(), Size: "3GB"}}
			if !lazy {
				cfg.ManagedToolchainRoot = root
			}
			if err := client.CreateContainer(cfg); err != nil {
				t.Fatal(err)
