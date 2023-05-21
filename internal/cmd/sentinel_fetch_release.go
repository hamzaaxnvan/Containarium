//go:build !windows && !containarium_client

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/config"
	"github.com/spf13/cobra"
)

var (
	sentinelFetchReleaseSentinelURL string
	sentinelFetchReleaseTag         string
	sentinelFetchReleaseSecret      string
)

