package mcp

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/footprintai/containarium/internal/sshconfig"
	"github.com/footprintai/containarium/pkg/core/incus"
)

// handleSyncSSHConfig is the agent-native version of `containarium
// ssh-config sync`. It lets the agent wire SSH aliases without needing
// the CLI binary installed on the operator's machine. Same internal
// generator, different invocation surface — preserves the CLI-first
// principle (one Go function, two surfaces) from CLAUDE.md.
func handleSyncSSHConfig(client API, args map[string]interface{}) (string, error) {
	// Resolve output path. Default lives under $HOME so it works the
	// same way the CLI version does — both produce a file that the
	// user's ~/.ssh/config can Include.
	out := getStringArg(args, "out", "")
	if out == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		out = filepath.Join(home, ".containarium", "ssh_config")
	}

	containers, err := fetchContainersForSSHConfig(client)
	if err != nil {
		return "", err
	}

	opts := sshconfig.Options{
		Sentinel:       getStringArg(args, "sentinel", ""),
		IdentityFile:   getStringArg(args, "identity_file", ""),
		IncludeStopped: getBoolArg(args, "include_stopped", false),
	}
