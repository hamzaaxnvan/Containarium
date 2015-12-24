package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

const minerSignaturesPath = "/v1/security/miner-signatures"

// minerSignatureTools (#2763): list / add / remove over the REST routes that
// `containarium security miner-signatures list|add|remove` (#2638) calls.
// Thin wrappers: same endpoints, generated protobuf types, and the kind as
// the proto enum (validated here, so free text is never sent).
func minerSignatureTools() []Tool {
	kindSchema := map[string]interface{}{
		"type":        "string",
		"enum":        []string{"name", "argv", "sha256"},
		"description": "Matcher kind: name (process name), argv (command-line marker) or sha256 (binary digest).",
	}
	patternSchema := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": desc}
	}
	return []Tool{
		{
			Name: "list_miner_signatures",
			Description: "List the merged miner-signature list the miner rule matches against: the " +
				"baseline shipped with the daemon (`builtin: true`) plus operator-added entries. " +
				"Read-only. Mirrors `containarium security miner-signatures list`.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
			Handler:     handleListMinerSignatures,
		},
