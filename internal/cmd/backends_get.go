package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var backendsGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Show a single backend's details by ID",
	Long: `Fetch one backend's metadata by ID — same shape as 'backends list'
but filtered to a single host. Returns a clear "not found" if the ID
doesn't match any registered backend.

Use this when you have a backend ID from 'backends list' or from a
container's BackendID field and want to drill down without paging
through the full list.

Example:
  containarium backends get tunnel-node-a-gpu --server <host:port>`,
	Args: cobra.ExactArgs(1),
	RunE: runBackendsGet,
}

