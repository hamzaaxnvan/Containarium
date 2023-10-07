package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestListSecretsDecodesGatewayCamelCase is the regression guard for
// #1219.
//
// The daemon serves this endpoint through grpc-gateway, whose marshaller
// is configured with `UseProtoNames: false`
// (internal/gateway/gateway.go). protojson therefore emits **lowerCamelCase**
// field names: `createdAt`, `updatedAt`.
//
// The client used to return []map[string]interface{} and the CLI read
// `row["updated_at"]` — a key the server never sends. That lookup yields
// nil, so `containarium secrets list --http` printed `<nil>` in its
// UPDATED column. Nothing failed; the column was simply wrong.
//
// That is the whole argument for typing this response: a snake_case/
// camelCase mismatch is invisible to a map and a compile-or-decode
// concern for a struct.
func TestListSecretsDecodesGatewayCamelCase(t *testing.T) {
	// Exactly what grpc-gateway emits for ListSecretsResponse.
	const gatewayJSON = `{
	  "secrets": [
	    {
	      "username": "alice",
	      "name": "API_KEY",
	      "version": 3,
	      "createdAt": "2026-08-01T10:00:00Z",
	      "updatedAt": "2026-08-07T12:30:00Z",
	      "delivery": "compose",
	      "deliveryMode": "SECRET_DELIVERY_COMPOSE"
	    },
	    {
	      "username": "alice",
	      "name": "DB_PASSWORD",
	      "version": 1,
	      "createdAt": "2026-08-02T09:00:00Z",
