package cmd

import (
	"strings"
	"testing"
)

func TestExtractUsernameFromContainerName(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		wantUsername  string
		wantValid     bool
	}{
		{
			name:          "standard container name",
			containerName: "alice-container",
			wantUsername:  "alice",
			wantValid:     true,
		},
		{
			name:          "hyphenated username",
			containerName: "alice-dev-container",
			wantUsername:  "alice-dev",
			wantValid:     true,
		},
		{
			name:          "numeric suffix in username",
			containerName: "user123-container",
			wantUsername:  "user123",
			wantValid:     true,
		},
		{
			name:          "long username",
			containerName: "very-long-username-with-many-parts-container",
			wantUsername:  "very-long-username-with-many-parts",
			wantValid:     true,
		},
		{
			name:          "single char username",
			containerName: "a-container",
			wantUsername:  "a",
			wantValid:     true,
		},
		{
			name:          "no container suffix",
			containerName: "alice",
			wantUsername:  "",
			wantValid:     false,
		},
		{
			name:          "different suffix",
			containerName: "alice-dev",
			wantUsername:  "",
			wantValid:     false,
		},
		{
			name:          "system container prefix",
			containerName: "_system-container",
			wantUsername:  "_system",
			wantValid:     true, // valid format but system containers should be filtered elsewhere
		},
		{
			name:          "empty string",
			containerName: "",
			wantUsername:  "",
			wantValid:     false,
		},
		{
			name:          "only suffix",
			containerName: "-container",
			wantUsername:  "",
			wantValid:     true, // technically valid format, empty username
		},
		{
			name:          "container in middle",
			containerName: "alice-container-backup",
			wantUsername:  "",
			wantValid:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotUsername, gotValid := extractUsernameFromContainerName(tt.containerName)
			if gotUsername != tt.wantUsername {
				t.Errorf("extractUsernameFromContainerName(%q) username = %q, want %q",
					tt.containerName, gotUsername, tt.wantUsername)
			}
			if gotValid != tt.wantValid {
				t.Errorf("extractUsernameFromContainerName(%q) valid = %v, want %v",
					tt.containerName, gotValid, tt.wantValid)
			}
		})
	}
}

// extractUsernameFromContainerName extracts the username from a container name
// following the Containarium naming convention: {username}-container
func extractUsernameFromContainerName(containerName string) (username string, valid bool) {
	const suffix = "-container"
	if strings.HasSuffix(containerName, suffix) {
		return strings.TrimSuffix(containerName, suffix), true
	}
	return "", false
}

func TestValidateSSHKeyFormat(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		isValid bool
	}{
		// Valid key types
		{
			name:    "RSA key",
			key:     "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC7... user@host",
			isValid: true,
		},
		{
			name:    "ED25519 key",
			key:     "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGj... user@host",
			isValid: true,
		},
		{
			name:    "ECDSA key",
			key:     "ssh-ecdsa AAAAE2VjZHNhLXNoYTItbmlzdHA... user@host",
			isValid: true,
		},
		{
			name:    "ecdsa-sha2-nistp256",
			key:     "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTI... user@host",
			isValid: true,
		},
		{
			name:    "ecdsa-sha2-nistp384",
			key:     "ecdsa-sha2-nistp384 AAAAE2VjZHNhLXNoYTI... user@host",
			isValid: true,
		},
		{
			name:    "ecdsa-sha2-nistp521",
			key:     "ecdsa-sha2-nistp521 AAAAE2VjZHNhLXNoYTI... user@host",
			isValid: true,
		},
		{
			name:    "DSS key (legacy)",
			key:     "ssh-dss AAAAB3NzaC1kc3MAAACBAP... user@host",
			isValid: true,
		},

		// Invalid keys
		{
			name:    "empty string",
			key:     "",
			isValid: false,
		},
		{
			name:    "random text",
			key:     "this is not an ssh key",
			isValid: false,
		},
		{
			name:    "looks like key but wrong prefix",
			key:     "rsa-key AAAAB3NzaC1yc2E... user@host",
			isValid: false,
		},
		{
			name:    "PEM format (not supported)",
			key:     "-----BEGIN RSA PUBLIC KEY-----",
			isValid: false,
		},
		{
			name:    "comment only",
			key:     "# this is a comment",
			isValid: false,
		},
		{
			name:    "key with extra prefix",
			key:     "command=\"/bin/false\" ssh-rsa AAAA... user@host",
			isValid: false, // options are not supported in our simple check
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidSSHKeyFormat(tt.key)
			if got != tt.isValid {
				t.Errorf("isValidSSHKeyFormat(%q) = %v, want %v", tt.key, got, tt.isValid)
			}
		})
	}
}

// isValidSSHKeyFormat checks if a string looks like a valid SSH public key
func isValidSSHKeyFormat(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}

	validPrefixes := []string{
		"ssh-rsa ",
		"ssh-ed25519 ",
		"ssh-ecdsa ",
		"ecdsa-sha2-",
		"ssh-dss ",
	}

	for _, prefix := range validPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func TestSyncAccountsSummary(t *testing.T) {
	tests := []struct {
		name     string
		restored int
		skipped  int
		failed   int
		wantErr  bool
	}{
		{
			name:     "all successful",
			restored: 5,
			skipped:  0,
			failed:   0,
			wantErr:  false,
		},
		{
			name:     "some skipped",
			restored: 3,
			skipped:  2,
			failed:   0,
			wantErr:  false,
		},
		{
			name:     "some failed",
