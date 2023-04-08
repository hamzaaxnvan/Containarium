package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

const processPolicyPath = "/v1/security/process-policy"

// processPolicyTools (#2666): get / set / release over the process-policy
// REST routes that `containarium security process-policy get|set|release`
// (#2636) calls. Thin wrappers: the same endpoints, the generated protobuf
// request/response types, and the action as the proto enum (validated here, so
// free text is never sent). The mining-behavior thresholds stay CLI-only.
func processPolicyTools() []Tool {
	return []Tool{
		{
			Name: "security_process_policy_get",
			Description: "Show what a HIGH mining finding does to its container: the stored process " +
				"policy and the effective policy the responder applies (action ALERT, THROTTLE or " +
				"FREEZE, throttle allowance, TTL backstop). Without `container` reads the backend " +
				"default; with `container` reads that container's override (none stored means it " +
				"inherits the default). Read-only. Mirrors `containarium security process-policy get`.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"container": map[string]interface{}{
						"type":        "string",
						"description": "Container whose override to read; omit for the backend default.",
					},
				},
			},
			Handler: handleSecurityProcessPolicyGet,
		},
		{
			Name: "security_process_policy_set",
			Description: "Set what a HIGH mining finding does to its container. Without `container` " +
				"this writes the backend default; WITH `container` it writes an override for that " +
				"container only (`clear_override` removes it). `action` is ALERT (record and notify " +
				"only), THROTTLE (cap CPU to `throttle_allowance`) or FREEZE (pause the container); " +
				"THROTTLE and FREEZE are reversible with security_process_policy_release and are " +
				"released automatically after `ttl`. Omitted fields inherit. Admin-only. Mirrors " +
				"`containarium security process-policy set` (mining-behavior thresholds are CLI-only). `kill_process` also kills the matched miner process on a miner-signature finding; omit it to inherit.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"ALERT", "THROTTLE", "FREEZE"},
						"description": "What a HIGH mining finding does to the container.",
					},
					"throttle_allowance": map[string]interface{}{
						"type":        "string",
						"description": "limits.cpu.allowance applied by THROTTLE: \"10%\" or \"10ms/100ms\".",
					},
					"ttl": map[string]interface{}{
						"type":        "string",
						"description": "How long an unreleased action stays before the TTL backstop releases it, e.g. \"24h\".",
					},
					"kill_process": map[string]interface{}{
						"type":        "boolean",
						"description": "Also kill the matched miner process on a HIGH miner-signature finding. Omit to inherit (an explicit false turns the kill off).",
					},
					"container": map[string]interface{}{
						"type":        "string",
						"description": "Write this container's override instead of the backend default.",
					},
					"clear_override": map[string]interface{}{
						"type":        "boolean",
						"description": "Remove the container's override (requires `container`; cannot be combined with policy fields).",
					},
				},
			},
			Handler: handleSecurityProcessPolicySet,
		},
		{
			Name: "security_process_policy_release",
			Description: "Undo the open throttle or freeze on a container: restores the CPU limit " +
				"recorded when it was throttled, or unfreezes it, marks the action released by the " +
				"operator and resolves its finding. A miner-signature finding shared with another " +
				"box of the tenant that still has an action open or a response queued is kept open " +
				"instead (`finding_kept_open: true`; the result's first line says \"Finding #N kept " +
				"open: another box still has a response\") and is resolved when the last box is " +
				"released. Fails (404) when no action is open on the container. Admin-only. " +
				"Mirrors `containarium security process-policy release`.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
