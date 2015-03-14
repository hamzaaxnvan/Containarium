package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// peerBackendFor serves one peer whose /v1/system/info answers with the given
// JSON body and returns that peer's entry from ListBackends.
func peerBackendFor(t *testing.T, systemInfoJSON string) *pb.BackendInfo {
	t.Helper()
	peerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/system/info" {
