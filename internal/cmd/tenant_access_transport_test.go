package cmd

import (
	"github.com/footprintai/containarium/internal/client"
	"testing"
)

func TestTenantReviewNetworkPolicyHTTPDefaultTransport(t *testing.T) {
	oldServer, oldHTTP, oldInsecure := serverAddr, httpMode, insecure
	t.Cleanup(func() { serverAddr, httpMode, insecure = oldServer, oldHTTP, oldInsecure })
	serverAddr, httpMode, insecure = "http://127.0.0.1:12345", false, true
	c, err := newTenantAccessClient()
	if err != nil {
