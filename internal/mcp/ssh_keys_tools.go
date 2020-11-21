package mcp

import (
	"fmt"
	"net/url"

	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// sshKeysTools (#2945): a thin wrapper over PUT /v1/containers/{username}/ssh-keys
// (SetSSHKeys), the call `containarium ssh-keys set` also makes.
func sshKeysTools() []Tool {
	return []Tool{
		{
			Name: "set_ssh_keys",
			Description: "Replace a box user's authorized SSH keys with exactly the given list. " +
				"Keys not in the list are removed from the box. A removed key is also revoked in the key registry when this box user owns its registry entry (the first account to register the key, counting this user's own collaborator accounts; a revoked entry is replaced by the next registration) or the entry has no owner, and no other account, including a collaborator account, still holds the same public key. Otherwise it is only removed from this box and the entry stays active; if this box user owned it, the entry loses its owner, so the next account to add the key takes it over and the last holder's own removal revokes it. This is not a guaranteed revocation of a compromised key.Keys the platform manages itself " +
				"are always kept. Idempotent. Every key must be a single-line public key with no " +
				"options (command=, from=); one invalid key fails the whole call with no change. " +
				"An empty list removes every caller-managed key and needs allow_empty=true. " +
				"Same access as adding a key.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{"type": "string", "description": "Username of the box"},
					"ssh_public_keys": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "The complete desired list of public keys",
					},
					"allow_empty": map[string]interface{}{
						"type":        "boolean",
						"description": "Must be true to send an empty list (removes every caller-managed key)",
					},
				},
				"required": []string{"username"},
			},
			Handler: handleSetSSHKeys,
		},
	}
}

func handleSetSSHKeys(client API, args map[string]interface{}) (string, error) {
	username := getStringArg(args, "username", "")
	if username == "" {
		return "", fmt.Errorf("username is required")
