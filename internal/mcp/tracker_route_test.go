package mcp

// tracker_route_list (#2021) — thin wrapper over the same REST endpoint
// `containarium tracker route list` calls.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
