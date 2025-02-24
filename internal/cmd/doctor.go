//go:build !windows && !containarium_client

package cmd

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/footprintai/containarium/internal/hostcheck"
	"github.com/footprintai/containarium/internal/policyreadiness"
)

// doctor — the host capability self-check from the daemon deploy contract
// (prd/oss/daemon-deploy-contract.md). It catches the "capability trap": a
// systemd unit that looks fine (User=root) but whose NoNewPrivileges /
// missing AmbientCapabilities / missing ReadWritePaths silently break
// `useradd` — so the daemon starts "successfully" and only fails on the
// FIRST container create, hours later. The definitive check is a live
// useradd/userdel probe.
//
// The check logic lives in internal/hostcheck so the daemon's cloud status
// probe can run the same checks (#528); this file is the CLI shell.
//
// Run it under the SAME constraints as the daemon (i.e. via the systemd
// unit, or as the daemon) for an accurate result — a plain root shell has
// full caps and will pass even if the daemon unit is mis-configured.

var doctorCmd = &cobra.Command{
	Use:   "doctor",
