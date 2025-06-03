package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// The gateway's JSON for GetNetworkPolicyStatusResponse decodes into the
// generated type, so the CLI cannot drift from the contract.
func TestDecodeStatus_GatewayJSON(t *testing.T) {
	raw := []byte(`{"status":{"tenant":"acme","state":"NETWORK_POLICY_APPLIED_STATE_LOADED",
		"effectiveMode":"NETWORK_POLICY_MODE_LOG_ONLY","effectiveEgressMode":"NETWORK_POLICY_MODE_ENFORCE",
		"lastReconciledAt":"2026-10-23T10:00:00Z","enforcementArmed":false,"boxesLoaded":2,"boxesNotLoaded":1,
		"uncoveredPaths":["NETWORK_POLICY_UNCOVERED_PATH_IPV6","NETWORK_POLICY_UNCOVERED_PATH_DOMAIN_REFRESH_LAG"],
		"aFieldFromANewerDaemon":true}}`)
	s, err := decodeStatus(raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.GetTenant() != "acme" || s.GetBoxesLoaded() != 2 || s.GetBoxesNotLoaded() != 1 || len(s.GetUncoveredPaths()) != 2 {
		t.Errorf("decoded = %v", s)
	}
	if s.GetEffectiveEgressMode() != pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE {
		t.Errorf("effective egress mode = %v", s.GetEffectiveEgressMode())
	}
	if !s.GetLastReconciledAt().AsTime().Equal(time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("last_reconciled_at = %v", s.GetLastReconciledAt())
	}
}

// A tenant name is one path segment, whatever it contains.
func TestNetworkPolicyStatusURL_EscapesTenant(t *testing.T) {
	got := networkPolicyStatusURL("http://localhost:8080/", "a/b c")
	if want := "http://localhost:8080/v1/network-policies/a%2Fb%20c/status"; got != want {
		t.Errorf("url = %q, want %q", got, want)
	}
}

func TestPrintStatus_PartiallyLoadedOnUnarmedHost(t *testing.T) {
	var b bytes.Buffer
	printStatus(&b, &pb.NetworkPolicyStatus{
		Tenant:              "acme",
		State:               pb.NetworkPolicyAppliedState_NETWORK_POLICY_APPLIED_STATE_PARTIALLY_LOADED,
		EffectiveMode:       pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY,
		EffectiveEgressMode: pb.NetworkPolicyMode_NETWORK_POLICY_MODE_LOG_ONLY,
