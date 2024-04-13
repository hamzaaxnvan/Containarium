package gatewayprovider

import (
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestName_CoversEveryEnumValue is the guard against a new GatewayProvider value
// shipping without a provider name. The enum is the contract, so every value in
// it must resolve — and must round-trip, or the CLI's --provider vocabulary and
// the wire enum have drifted.
func TestName_CoversEveryEnumValue(t *testing.T) {
	if len(pb.GatewayProvider_name) < 2 {
		t.Fatal("the enum has no values besides UNSPECIFIED — test harness broken")
	}
	for value, enumName := range pb.GatewayProvider_name {
		p := pb.GatewayProvider(value)
		got, err := Name(p)

		if p == pb.GatewayProvider_GATEWAY_PROVIDER_UNSPECIFIED {
			if err == nil {
				t.Errorf("UNSPECIFIED resolved to %q; it must be rejected, never defaulted", got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s (%d) has no provider name: %v", enumName, value, err)
