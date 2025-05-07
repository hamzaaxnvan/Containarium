package client

import (
	"context"
	"errors"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTenantReviewUnknownFieldsAndAbsentPolicyHTTP(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/network-tenants/capabilities":
			w.Write([]byte(`{"contract":"native-network-tenant/v1","backendScope":"local-lxc","nativeMembershipRead":true,"sharedTenantCreate":true,"futureFlag":true}`))
		case "/v1/network-policies/missing":
			http.NotFound(w, r)
