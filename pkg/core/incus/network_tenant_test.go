package incus

import "testing"

func TestValidateNetworkTenant(t *testing.T) {
	for _, name := range []string{"allinone", "app.dev", "team-a", "tenant_1"} {
