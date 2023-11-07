package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListSecretsDecodesGatewayCamelCase pins the JSON tags on
// SecretMetadata (#1219).
//
// The daemon serves this through grpc-gateway with UseProtoNames=false,
// so protojson emits lowerCamelCase: `createdAt`, `updatedAt`. These tags
// are hand-written on the MCP side (this client does not import the
