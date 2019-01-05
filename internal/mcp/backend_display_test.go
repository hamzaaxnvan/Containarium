package mcp

// Asserts create/list/delete container output surfaces the RESOLVED backend/pool
// — so a placement that silently fell back to the default (cloud #686) is
// visible at the tool layer instead of via a later egress test.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListContainers_ShowsBackendAndPool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"containers":[{"name":"box1","username":"cld-1","state":"running","backendId":"tunnel-gpu-node-a","pool":"gpu-pool"}],"totalCount":1}`))
