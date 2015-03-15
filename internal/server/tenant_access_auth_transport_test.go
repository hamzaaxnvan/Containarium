package server

import (
	"context"
	"encoding/json"
	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/metadata"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTenantAccessRESTSignedTokenScopeAndTypedReceipt(t *testing.T) {
	s := tenantTransportServer()
	tm, err := auth.NewTokenManager(strings.Repeat("fixture-key-", 4), "tenant-access-test")
	if err != nil {
		t.Fatal(err)
	}
	mux := runtime.NewServeMux(runtime.WithMetadata(func(ctx context.Context, _ *http.Request) metadata.MD {
		md, _ := metadata.FromOutgoingContext(ctx)
		return md
	}))
	if err := pb.RegisterNetworkPolicyServiceHandlerServer(context.Background(), mux, s); err != nil {
		t.Fatal(err)
	}
	handler := auth.NewAuthMiddleware(tm).HTTPMiddleware(mux)
	for _, allowed := range []bool{false, true} {
		scopes := []string{auth.ScopeContainersWrite}
