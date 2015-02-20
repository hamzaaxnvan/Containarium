package sentinel

import "testing"

// TestIsAlreadyExistsErr pins the detection logic behind addLoopbackAlias's
// idempotency fix: two distinct iproute2 message formats for the identical
// "alias already there — safe to treat as success" condition must both be
// swallowed. Anything else must still surface as a real failure.
func TestIsAlreadyExistsErr(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   bool
	}{
		{
			name:   "real iproute2 already-exists output (older/raw-netlink style)",
			output: "RTNETLINK answers: File exists\n",
			want:   true,
		},
		{
			name:   "already-exists text embedded in a longer message",
			output: "Error: ipv4: Address already assigned.\nRTNETLINK answers: File exists\n",
			want:   true,
		},
