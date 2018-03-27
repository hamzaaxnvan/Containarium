package container

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/collaborator"
	"github.com/footprintai/containarium/pkg/core/ospkg"
	"github.com/footprintai/containarium/pkg/core/ostype"
)

// CollaboratorManager handles collaborator operations for containers
type CollaboratorManager struct {
	manager *Manager
	store   *collaborator.Store
}

// NewCollaboratorManager creates a new collaborator manager
func NewCollaboratorManager(manager *Manager, store *collaborator.Store) *CollaboratorManager {
	return &CollaboratorManager{
		manager: manager,
		store:   store,
	}
}

// AddCollaborator adds a collaborator to a container
// This creates:
// 1. A user in the container with the name {container-name}-{collaborator-username}
// 2. Sudoers configuration allowing passwordless sudo su to the container owner
// 3. Session logging for audit trail
// 4. Jump server account for SSH ProxyJump access
// 5. Persistence record in PostgreSQL
func (cm *CollaboratorManager) AddCollaborator(ownerUsername, collaboratorUsername string, sshPublicKeys []string, grantSudo, grantContainerRuntime bool) (*collaborator.Collaborator, error) {
	// Validate inputs
	if ownerUsername == "" {
		return nil, fmt.Errorf("owner username cannot be empty")
	}
	if collaboratorUsername == "" {
		return nil, fmt.Errorf("collaborator username cannot be empty")
	}
	// Normalize keys: drop blanks/whitespace, require at least one. All
	// the collaborator's keys are authorized so they can connect from
	// any of their machines (#369).
	keys := make([]string, 0, len(sshPublicKeys))
	for _, k := range sshPublicKeys {
		if k = strings.TrimSpace(k); k != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("at least one SSH public key is required")
	}

	// Validate owner username (same rules — prevents sudoers injection)
	if !isValidCollaboratorUsername(ownerUsername) {
		return nil, fmt.Errorf("invalid owner username: must contain only letters, numbers, dash, and underscore")
	}

	// Validate collaborator username
	if !isValidCollaboratorUsername(collaboratorUsername) {
		return nil, fmt.Errorf("invalid collaborator username: must contain only letters, numbers, dash, and underscore")
	}

	containerName := ownerUsername + "-container"
	accountName := containerName + "-" + collaboratorUsername

	// Validate account name length (Linux username limit is 32 characters)
	if len(accountName) > 32 {
		return nil, fmt.Errorf("account name %q exceeds Linux 32-character limit (%d chars); use shorter usernames", accountName, len(accountName))
	}

	// Check if container exists and is running
	info, err := cm.manager.incus.GetContainer(containerName)
	if err != nil {
		return nil, fmt.Errorf("container not found: %w", err)
	}
	if info.State != "Running" {
		return nil, fmt.Errorf("container must be running to add collaborator, current state: %s", info.State)
	}

	// Step 1: Create collaborator user in the container
	if err := cm.createCollaboratorUser(containerName, ownerUsername, accountName, keys, grantSudo, grantContainerRuntime); err != nil {
		return nil, fmt.Errorf("failed to create collaborator user in container: %w", err)
	}

	// Step 2: Create jump server account for SSH ProxyJump access.
	// Seed it with the first key, then authorize the rest so the
	// collaborator can ProxyJump from any of their machines (#369).
	if err := CreateJumpServerAccount(accountName, keys[0], true); err != nil {
		// Rollback: remove user from container
		_ = cm.removeCollaboratorUser(containerName, accountName)
		return nil, fmt.Errorf("failed to create jump server account: %w", err)
	}
	for _, k := range keys[1:] {
		if err := updateUserSSHKey(accountName, k, true); err != nil {
			// Rollback: container user + jump account.
			_ = cm.removeCollaboratorUser(containerName, accountName)
			_ = DeleteJumpServerAccount(accountName, true)
			return nil, fmt.Errorf("failed to authorize additional SSH key on jump server: %w", err)
		}
	}

	// Step 3: Store collaborator in database
	collab := &collaborator.Collaborator{
		ContainerName:        containerName,
		OwnerUsername:        ownerUsername,
		CollaboratorUsername: collaboratorUsername,
		AccountName:          accountName,
		SSHPublicKey:         strings.Join(keys, "\n"),
		CreatedAt:            time.Now(),
		CreatedBy:            ownerUsername,
		HasSudo:              grantSudo,
		HasContainerRuntime:  grantContainerRuntime,
	}

	if err := cm.store.Add(context.Background(), collab); err != nil {
		// Rollback: remove user from container and jump server
		_ = cm.removeCollaboratorUser(containerName, accountName)
		_ = DeleteJumpServerAccount(accountName, true)
		return nil, fmt.Errorf("failed to store collaborator: %w", err)
	}

	return collab, nil
}

// createCollaboratorUser creates a collaborator user inside the container
func (cm *CollaboratorManager) createCollaboratorUser(containerName, ownerUsername, accountName string, sshPublicKeys []string, grantSudo, grantContainerRuntime bool) error {
	// Detect OS family from container labels or by probing
	info, err := cm.manager.incus.GetContainer(containerName)
	family := ostype.Debian
	if err == nil {
		if osLabel, ok := info.Labels[ostype.OSTypeLabelKey]; ok {
			family = ostype.FamilyFromLabel(osLabel)
		} else {
			family = ostype.DetectFamily(cm.manager.incus, containerName)
		}
	}
	pkgMgr := ospkg.ForFamily(family)

	// Create user (OS-aware: adduser on Debian, useradd on RHEL)
	gecos := fmt.Sprintf("Collaborator %s", accountName)
	if err := cm.manager.incus.Exec(containerName, pkgMgr.CreateUserCmd(accountName, gecos)); err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	// Configure sudoers with session logging
	var sudoersContent string
	if grantSudo {
		sudoersContent = cm.generateFullSudoers(accountName)
	} else {
		sudoersContent = cm.generateCollaboratorSudoers(accountName, ownerUsername)
	}
	sudoersPath := fmt.Sprintf("/etc/sudoers.d/%s", accountName)

	if err := cm.manager.incus.WriteFile(containerName, sudoersPath, []byte(sudoersContent), "0440"); err != nil {
		return fmt.Errorf("failed to configure sudoers: %w", err)
	}

	// Create sudo I/O log directory
	logDir := fmt.Sprintf("/var/log/sudo-io/%s", accountName)
	if err := cm.manager.incus.Exec(containerName, []string{"mkdir", "-p", logDir}); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}
	if err := cm.manager.incus.Exec(containerName, []string{"chmod", "750", logDir}); err != nil {
		return fmt.Errorf("failed to set log directory permissions: %w", err)
	}

	// Grant container runtime access (add to docker and/or podman groups)
	if grantContainerRuntime {
		// Add to docker group (if it exists)
		_ = cm.manager.incus.Exec(containerName, []string{"usermod", "-aG", "docker", accountName})
		// Add to podman group (if it exists)
		_ = cm.manager.incus.Exec(containerName, []string{"usermod", "-aG", "podman", accountName})
	}

	// Setup SSH keys — all of the collaborator's keys (#369).
	if err := cm.manager.addSSHKeys(containerName, accountName, sshPublicKeys); err != nil {
		return fmt.Errorf("failed to add SSH keys: %w", err)
	}

	// Create logrotate config for sudo-io logs (if it doesn't exist)
	logrotateConfig := `/var/log/sudo-io/*/* {
    daily
    missingok
    rotate 90
    compress
    delaycompress
    notifempty
    create 640 root adm
}
`
	_ = cm.manager.incus.WriteFile(containerName, "/etc/logrotate.d/sudo-io", []byte(logrotateConfig), "0644")

	return nil
}

// generateFullSudoers generates sudoers configuration granting full sudo access with session logging
func (cm *CollaboratorManager) generateFullSudoers(accountName string) string {
	return fmt.Sprintf(`# Collaborator %s - full sudo access with session logging
Defaults:%s log_input, log_output
Defaults:%s iolog_dir=/var/log/sudo-io/%s
Defaults:%s iolog_file=%%{user}.%%{seq}

# Allow full passwordless sudo access
%s ALL=(ALL) NOPASSWD: ALL
`,
		accountName,
		accountName,
		accountName, accountName,
		accountName,
		accountName,
