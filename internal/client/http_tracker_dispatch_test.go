package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Dispatch REST calls (#2022) — method + path (+ query) must match the
