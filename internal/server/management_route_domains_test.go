//go:build !containarium_client

package server

import (
	"testing"
)

// TestManagementRouteDomains covers #213: the daemon ensures one management
// route per PublicBaseDomains entry (so every parent domain's apex is Caddied
// + served), falling back to BaseDomain for single-domain deployments.
func TestManagementRouteDomains(t *testing.T) {
	tests := []struct {
		name string
		cfg  *DualServerConfig
		want []string
	}{
		{
			name: "single base domain (unchanged behavior)",
			cfg:  &DualServerConfig{BaseDomain: "example.org"},
