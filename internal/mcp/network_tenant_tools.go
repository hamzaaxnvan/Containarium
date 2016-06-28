package mcp

import (
	"encoding/json"
	"fmt"
)

type networkTenantCapabilitiesAPI interface {
	GetNetworkTenantCapabilities() (*NetworkTenantCapabilities, error)
}
type NetworkTenantCapabilities struct {
	Contract             string `json:"contract"`
	NativeMembershipRead bool   `json:"nativeMembershipRead"`
	SharedTenantCreate   bool   `json:"sharedTenantCreate"`
	AtomicTenantAccess   bool   `json:"atomicTenantAccess"`
	NetworkPolicyWrite   bool   `json:"networkPolicyWrite"`
	Reason               string `json:"reason"`
	BackendScope         string `json:"backendScope"`
}

func (c *Client) GetNetworkTenantCapabilities() (*NetworkTenantCapabilities, error) {
	var out NetworkTenantCapabilities
