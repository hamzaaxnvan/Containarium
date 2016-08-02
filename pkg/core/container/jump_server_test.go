package container

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestShadowAccountLocked(t *testing.T) {
	// Inputs are real getent-shadow shapes seen on a BYOC host. The crux:
	// "*" (what our unlock sets) and "" (NP sibling) are NOT locked and SSH
	// works; only "!"-prefixed is locked. `passwd -S` cannot tell "*" from "!"
	// (reports both as "L"), which is exactly why we parse the field directly.
	tests := []struct {
		name string
		line string
		want bool
	}{
		{"locked bang (the bug)", "cld-9c09f73a:!:20000:0:99999:7:::", true},
		{"locked bangbang", "u:!!:20000:0:99999:7:::", true},
		{"locked bang+hash", "u:!$6$abc$def:20000:0:99999:7:::", true},
		{"star = disabled not locked (works)", "cld-9c09f73a:*:20000:0:99999:7:::", false},
		{"empty NP sibling (works)", "apibox-dev-3090::20000:0:99999:7:::", false},
		{"real password not locked", "alice:$6$salt$hash:20000:0:99999:7:::", false},
		{"trailing newline locked", "bob:!:20000\n", true},
		{"empty input", "", false},
		{"no colon", "garbage", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shadowAccountLocked(tt.line); got != tt.want {
				t.Errorf("shadowAccountLocked(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestIsValidUsername(t *testing.T) {
	tests := []struct {
		name     string
		username string
		want     bool
	}{
		// Valid usernames
		{
			name:     "simple lowercase",
			username: "alice",
			want:     true,
		},
		{
			name:     "with numbers",
			username: "alice123",
			want:     true,
		},
		{
			name:     "with hyphen",
			username: "alice-dev",
			want:     true,
		},
		{
			name:     "with underscore",
			username: "alice_dev",
			want:     true,
		},
		{
			name:     "uppercase",
			username: "Alice",
			want:     true,
		},
		{
			name:     "mixed case with numbers",
			username: "Alice123",
			want:     true,
		},
		{
			name:     "single character",
			username: "a",
			want:     true,
		},
		{
			name:     "max length 32",
			username: strings.Repeat("a", 32),
			want:     true,
		},

		// Invalid usernames
		{
			name:     "empty string",
			username: "",
			want:     false,
		},
		{
			name:     "too long",
			username: strings.Repeat("a", 33),
			want:     false,
		},
		{
			name:     "starts with number",
			username: "123alice",
			want:     false,
		},
		{
			name:     "starts with hyphen",
			username: "-alice",
			want:     false,
		},
		{
			name:     "contains space",
			username: "alice dev",
			want:     false,
		},
		{
			name:     "contains special char @",
			username: "alice@dev",
			want:     false,
		},
		{
			name:     "contains special char .",
			username: "alice.dev",
			want:     false,
		},
		{
			name:     "contains special char !",
			username: "alice!",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidUsername(tt.username)
			if got != tt.want {
				t.Errorf("isValidUsername(%q) = %v, want %v", tt.username, got, tt.want)
			}
		})
	}
}

func TestUserExists(t *testing.T) {
	// Test with a user that definitely exists on Unix systems
	t.Run("root user exists", func(t *testing.T) {
		// root should exist on all Unix systems
		if got := UserExists("root"); !got {
			t.Errorf("UserExists(\"root\") = false, want true")
		}
	})

	t.Run("nonexistent user", func(t *testing.T) {
		// This user should not exist
		if got := UserExists("nonexistent_user_12345"); got {
			t.Errorf("UserExists(\"nonexistent_user_12345\") = true, want false")
		}
	})
}

func TestParseSSHKeyFromAuthorizedKeys(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantKey string
		wantErr bool
	}{
		{
			name:    "single RSA key",
			content: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC... user@host",
			wantKey: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC... user@host",
			wantErr: false,
		},
		{
			name:    "single ED25519 key",
			content: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGj... user@host",
			wantKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGj... user@host",
			wantErr: false,
		},
		{
			name:    "single ECDSA key",
			content: "ssh-ecdsa AAAAE2VjZHNhLXNoYTItbmlzdHAyN... user@host",
			wantKey: "ssh-ecdsa AAAAE2VjZHNhLXNoYTItbmlzdHAyN... user@host",
			wantErr: false,
		},
		{
			name:    "ecdsa-sha2-nistp256 key",
			content: "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTI... user@host",
			wantKey: "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTI... user@host",
			wantErr: false,
		},
		{
			name: "multiple keys returns first",
			content: `ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC... first@host
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGj... second@host`,
			wantKey: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC... first@host",
			wantErr: false,
		},
		{
			name: "ignores comments",
			content: `# This is a comment
ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC... user@host`,
			wantKey: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC... user@host",
			wantErr: false,
		},
		{
			name: "ignores empty lines",
			content: `

ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGj... user@host

`,
			wantKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGj... user@host",
			wantErr: false,
		},
		{
			name:    "empty content",
			content: "",
			wantKey: "",
			wantErr: true,
		},
		{
			name:    "only comments",
			content: "# This is a comment\n# Another comment",
			wantKey: "",
			wantErr: true,
		},
		{
			name:    "invalid key format",
			content: "not-a-valid-ssh-key some-data",
			wantKey: "",
			wantErr: true,
		},
		{
			name:    "key with leading whitespace",
			content: "  ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC... user@host",
			wantKey: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC... user@host",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSSHKeyFromContent(tt.content)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseSSHKeyFromContent() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.wantKey {
				t.Errorf("parseSSHKeyFromContent() = %q, want %q", got, tt.wantKey)
			}
		})
	}
}

// parseSSHKeyFromContent is a helper function extracted from ExtractSSHKeyFromContainer
// for easier testing. It parses the content of an authorized_keys file and returns
// the first valid SSH public key.
func parseSSHKeyFromContent(content string) (string, error) {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Validate it looks like an SSH key
		if strings.HasPrefix(line, "ssh-rsa ") ||
			strings.HasPrefix(line, "ssh-ed25519 ") ||
			strings.HasPrefix(line, "ssh-ecdsa ") ||
			strings.HasPrefix(line, "ecdsa-sha2-") ||
			strings.HasPrefix(line, "ssh-dss ") {
			return line, nil
		}
	}

	return "", errNoValidSSHKey
}

var errNoValidSSHKey = &parseError{"no valid SSH key found in authorized_keys"}

type parseError struct {
	msg string
}

func (e *parseError) Error() string {
	return e.msg
}

func TestContainerNameToUsername(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		wantUsername  string
		wantValid     bool
	}{
		{
			name:          "standard container name",
