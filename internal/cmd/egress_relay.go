//go:build !windows && !containarium_client

package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/footprintai/containarium/internal/egressproxy"
	"github.com/spf13/cobra"
)
