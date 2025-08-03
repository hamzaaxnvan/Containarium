//go:build !windows && !containarium_client

package cmd

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/footprintai/containarium/internal/hypervisor"
	"github.com/footprintai/containarium/internal/sentinel"
	"github.com/spf13/cobra"
)

var (
	hvSentinelAddr string
	hvSentinelPin  string
	hvTunnelToken  string
	hvSpotID       string
	hvConsolePort  int
	hvConsoleToken string
	hvVBoxDir      string
)

var hypervisorAgentCmd = &cobra.Command{
