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
		case "/v1/network-policies/team-a":
			w.Write([]byte(`{"policy":{"tenant":"team-a","futurePolicyFlag":true},"revision":"fixture","futureFlag":true}`))
		case "/v1/network-policies/tenant-access":
			w.Write([]byte(`{"futureFlag":true}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer fixture.Close()
	c, err := NewHTTPClient(fixture.URL, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	cap, err := c.GetNetworkTenantCapabilities()
	if err != nil {
		t.Fatal(err)
	}
	if err = requireNativeTenantCapability(cap); err != nil {
		t.Fatal(err)
	}
	if _, err = c.GetNetworkPolicy("missing"); !errors.Is(err, ErrNetworkPolicyAbsent) {
		t.Fatal(err)
