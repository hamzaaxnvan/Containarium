// Package gatewayprovider maps the GatewayProvider proto enum to and from the
// provider names the model gateway's registry is keyed by.
//
// It exists as its own package because three layers need exactly the same
// mapping and none of them may import the others: the daemon's
// ModelGatewayServer (enum -> registry key), the typed client, and the CLI's
// --provider flag (name -> enum). The CLI is built with `-tags
// containarium_client`, which deliberately excludes internal/server, so "just
// export it from the server" is not available — and two copies of this mapping
// is exactly how a flag's vocabulary drifts from the contract.
//
// The mapping is DERIVED from the enum's own value names rather than written out
// by hand, so a new enum value cannot ship without a provider name:
//
//	GATEWAY_PROVIDER_GEMINI_OPENAI -> "gemini-openai"
//
// which is what modelgateway.DefaultProviders and ProvidersFromEnv
// (<PROVIDER>_UPSTREAM_URL) key on.
package gatewayprovider

import (
	"fmt"
