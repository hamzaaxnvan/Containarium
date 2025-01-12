package mcp

import (
	"encoding/json"
	"fmt"
)

type networkTenantCapabilitiesAPI interface {
	GetNetworkTenantCapabilities() (*NetworkTenantCapabilities, error)
}
type NetworkTenantCapabilities struct {
