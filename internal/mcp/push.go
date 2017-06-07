package mcp

import (
	"errors"
	"fmt"

	"github.com/footprintai/containarium/internal/transfer"
)

// sentinelHint turns transfer's bare "sentinel unresolved" failure into
// agent-native guidance. push/sync already auto-resolve the SSH target
// from the container's ssh_host, so when we still couldn't find one the
// agent should reach for get_container (to read ssh_host) or pass the
// `sentinel` arg explicitly — NOT go hunting for CONTAINARIUM_SENTINEL_HOST.
func sentinelHint(verb string, username string, err error) error {
	if errors.Is(err, transfer.ErrSentinelUnresolved) {
		return fmt.Errorf("%s failed: could not resolve the SSH target for %q. "+
			"Call get_container(%q) and read its ssh_host — that is the host to "+
			"reach. Pass it as the `sentinel` arg here, or use the `connect` tool "+
			"(exec mode) which resolves it for you. An empty ssh_host means a "+
			"direct / no-sentinel deployment (reach the container at its IP). "+
			"You do not need to set CONTAINARIUM_SENTINEL_HOST. (%w)",
			verb, username, username, err)
	}
	return fmt.Errorf("%s failed: %w", verb, err)
}

// handlePush is the agent-native version of `containarium push <user>`.
// Same Go function (transfer.Push) backs both surfaces; this just adapts
// the MCP args dict into a typed PushOptions struct.
func handlePush(client API, args map[string]interface{}) (string, error) {
	username := getStringArg(args, "username", "")
	if username == "" {
		return "", fmt.Errorf("username is required")
	}

	res, err := transfer.Push(transfer.PushOptions{
		Options: transfer.Options{
			Username:     username,
			SentinelHost: pickSentinel(client, args),
			KeyPath:      getStringArg(args, "key_path", ""),
			LocalPath:    getStringArg(args, "local_path", ""),
			RemotePath:   getStringArg(args, "remote_path", ""),
			Verbose:      false,
		},
		Branch:     getStringArg(args, "branch", ""),
		IncludeWIP: getBoolArg(args, "include_wip", false),
		DeployCmd:  getStringArg(args, "deploy_cmd", ""),
		RemoteName: getStringArg(args, "remote_name", ""),
	})
	if err != nil {
		return "", sentinelHint("push", username, err)
	}

