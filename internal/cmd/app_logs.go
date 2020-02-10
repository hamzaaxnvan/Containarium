package cmd

import (
	"fmt"

	"github.com/footprintai/containarium/internal/client"
	"github.com/spf13/cobra"
)

var (
	logsUsername  string
	logsTailLines int32
	logsFollow    bool
