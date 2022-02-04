package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
)

// fakeSystemInfoPeer serves /v1/system/info the way a real peer daemon does
// through grpc-gateway (protojson of GetSystemInfoResponse), or a 500 when
// fail is set. It counts requests and records the bearer token it was called
// with, so tests can pin both the per-peer call budget and that the caller's
// token is what gets forwarded.
type fakeSystemInfoPeer struct {
	srv       *httptest.Server
	calls     atomic.Int32
	authSeen  atomic.Value
	unrelated atomic.Int32
}

func newFakeSystemInfoPeer(t *testing.T, info *pb.SystemInfo, fail bool) *fakeSystemInfoPeer {
	t.Helper()
	body, err := protojson.Marshal(&pb.GetSystemInfoResponse{Info: info})
	if err != nil {
		t.Fatalf("marshal peer SystemInfo: %v", err)
	}
	p := &fakeSystemInfoPeer{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/system/info" {
			p.unrelated.Add(1)
			http.NotFound(w, r)
			return
		}
		p.calls.Add(1)
		p.authSeen.Store(r.Header.Get("Authorization"))
		if fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
