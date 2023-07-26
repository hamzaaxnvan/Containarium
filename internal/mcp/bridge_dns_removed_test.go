package mcp

import "testing"

// #2733 (slice B of #2263): the bridge_dns_status tool is removed together
// with its CLI counterpart and the RPC both wrapped.
func TestBridgeDNSStatusToolRemoved(t *testing.T) {
