package cmd

import (
	"context"
	"fmt"

	"github.com/footprintai/containarium/pkg/core/expose"
	"github.com/spf13/cobra"
)

var (
	exposePortPort        int
	exposePortDomain      string
	exposePortDescription string
)

var exposePortCmd = &cobra.Command{
