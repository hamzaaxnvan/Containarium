package mcp

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/footprintai/containarium/internal/gatewayprovider"
)

// Model-gateway mint, over the generated ModelGatewayService REST gateway
// (#1726). Same endpoint `containarium gateway mint` calls.

// MintGatewayTokenBody is the tool-facing request. Provider and TTL are taken in
// the shapes a human or an agent actually types — a lowercase provider name and a
// Go duration — and translated here into the wire shapes protojson requires (the
// enum's value NAME, and a seconds-suffixed Duration). Doing that translation in
// one place is why the tool handler stays free of protojson trivia.
type MintGatewayTokenBody struct {
	Box           string
	Provider      string
	RunID         string
	TTL           string
	AllowedModels []string
	DryRun        bool
}

// mintGatewayTokenWire is the protojson body of MintGatewayTokenRequest.
type mintGatewayTokenWire struct {
	Box           string   `json:"box"`
	Provider      string   `json:"provider"`
	RunID         string   `json:"runId,omitempty"`
	AllowedModels []string `json:"allowedModels,omitempty"`
	TTL           string   `json:"ttl,omitempty"`
	DryRun        bool     `json:"dryRun,omitempty"`
}

// MintGatewayTokenResponse is the minted token and where to point at it. Token
// and TokenID are empty on a dry run.
type MintGatewayTokenResponse struct {
	Token     string `json:"token"`
	BaseURL   string `json:"baseUrl"`
	KeyOwner  string `json:"keyOwner"`
	ExpiresAt string `json:"expiresAt"`
	TokenID   string `json:"tokenId"`
}

// MintGatewayToken mints a scoped gateway token for a box the caller owns.
func (c *Client) MintGatewayToken(req MintGatewayTokenBody) (*MintGatewayTokenResponse, error) {
	wire, err := req.toWire()
	if err != nil {
