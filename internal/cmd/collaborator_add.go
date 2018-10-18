package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// validSSHKeyPrefixes lists all accepted SSH public key prefixes, including FIDO keys.
var validSSHKeyPrefixes = []string{
	"ssh-",
	"ecdsa-",
	"sk-ssh-",
	"sk-ecdsa-",
}

var (
	collaboratorSSHKeyFiles  []string
	collaboratorGrantSudo    bool
	collaboratorGrantRuntime bool
)

var collaboratorAddCmd = &cobra.Command{
	Use:   "add <owner-username> <collaborator-username>",
	Short: "Add a collaborator to a container",
	Long: `Add a collaborator to a container.

The collaborator will be able to:
1. SSH into the container via their own account
2. Switch to the container owner's account using 'sudo su - <owner>'
3. All sessions are logged for auditing

Use --sudo to grant full sudo access (not just su - owner).
Use --container-runtime to add the collaborator to docker/podman groups.

A jump server account will also be created for SSH ProxyJump access.

Examples:
  # Add bob as a collaborator to alice's container
  containarium collaborator add alice bob --ssh-key ~/.ssh/bob.pub

  # Add carol as a collaborator using a different SSH key
  containarium collaborator add alice carol --ssh-key /path/to/carol.pub

  # Authorize several of bob's keys (one per machine)
  containarium collaborator add alice bob --ssh-key ~/.ssh/bob-laptop.pub --ssh-key ~/.ssh/bob-desktop.pub

  # Against a remote daemon
  containarium collaborator add alice bob --ssh-key ~/.ssh/bob.pub --server daemon.example.com:50051`,
	Args: cobra.ExactArgs(2),
	RunE: runCollaboratorAdd,
}

func init() {
	collaboratorCmd.AddCommand(collaboratorAddCmd)
	collaboratorAddCmd.Flags().StringArrayVar(&collaboratorSSHKeyFiles, "ssh-key", nil, "path to collaborator's SSH public key file (required; repeat to authorize multiple keys)")
	_ = collaboratorAddCmd.MarkFlagRequired("ssh-key")
	collaboratorAddCmd.Flags().BoolVar(&collaboratorGrantSudo, "sudo", false, "grant full sudo access (not just su - owner)")
	collaboratorAddCmd.Flags().BoolVar(&collaboratorGrantRuntime, "container-runtime", false, "add collaborator to docker/podman groups")
}

func runCollaboratorAdd(cmd *cobra.Command, args []string) error {
	ownerUsername := args[0]
	collaboratorUsername := args[1]

	// Read + validate each SSH public key file. All are authorized so
	// the collaborator can connect from any of their machines (#369).
	var sshPublicKeys []string
	for _, keyFile := range collaboratorSSHKeyFiles {
		sshKeyBytes, err := os.ReadFile(keyFile)
		if err != nil {
