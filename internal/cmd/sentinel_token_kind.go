//go:build !windows && !containarium_client

package cmd

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/footprintai/containarium/internal/auth"
)
