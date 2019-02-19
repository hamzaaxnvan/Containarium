package container

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateContainerName(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantErr   bool
		expectErr error
	}{
		// Valid names
		{
			name:    "valid simple name",
			input:   "alice",
			wantErr: false,
		},
		{
			name:    "valid with hyphen",
			input:   "alice-dev",
			wantErr: false,
		},
		{
			name:    "valid with numbers",
			input:   "alice-dev-2024",
			wantErr: false,
		},
		{
			name:    "valid with multiple hyphens",
			input:   "team-api-prod-v2",
			wantErr: false,
		},
		{
			name:    "valid single character",
			input:   "a",
			wantErr: false,
		},
		{
			name:    "valid all numbers",
			input:   "123456",
			wantErr: false,
		},

		// Invalid - reserved prefix
		{
			name:      "invalid system prefix",
			input:     "_containarium-core",
			wantErr:   true,
			expectErr: ErrReservedPrefix,
		},
		{
			name:      "invalid single underscore",
			input:     "_",
			wantErr:   true,
			expectErr: ErrReservedPrefix,
		},
		{
			name:      "invalid underscore prefix with valid name",
			input:     "_alice",
			wantErr:   true,
			expectErr: ErrReservedPrefix,
		},

		// Invalid - format
		{
			name:      "invalid uppercase",
			input:     "Alice",
			wantErr:   true,
			expectErr: ErrInvalidFormat,
		},
		{
			name:      "invalid with underscore in middle",
			input:     "alice_dev",
			wantErr:   true,
			expectErr: ErrInvalidFormat,
		},
		{
			name:      "invalid with space",
			input:     "alice dev",
			wantErr:   true,
			expectErr: ErrInvalidFormat,
		},
		{
			name:      "invalid with special chars",
			input:     "alice@dev",
			wantErr:   true,
			expectErr: ErrInvalidFormat,
		},
		{
			name:      "invalid with dot",
			input:     "alice.dev",
			wantErr:   true,
			expectErr: ErrInvalidFormat,
		},

		// Invalid - empty/length
		{
			name:      "invalid empty string",
			input:     "",
			wantErr:   true,
			expectErr: ErrEmpty,
		},
		{
			name:      "invalid too long",
			input:     strings.Repeat("a", 64),
			wantErr:   true,
			expectErr: ErrTooLong,
		},
		{
			name:    "valid at max length",
			input:   strings.Repeat("a", 63),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateContainerName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateContainerName() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.expectErr != nil && !errors.Is(err, tt.expectErr) {
				t.Errorf("ValidateContainerName() error = %v, expectErr %v", err, tt.expectErr)
			}
		})
	}
}

func TestIsSystemContainer(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "system container with valid name",
			input: "_containarium-core",
			want:  true,
		},
		{
			name:  "system container single underscore",
			input: "_",
			want:  true,
		},
		{
			name:  "user container",
			input: "alice",
			want:  false,
		},
		{
			name:  "user container with hyphen",
			input: "alice-dev",
			want:  false,
		},
		{
			name:  "empty string",
			input: "",
			want:  false,
		},
		{
			name:  "underscore in middle",
			input: "alice_dev",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsSystemContainer(tt.input)
			if got != tt.want {
				t.Errorf("IsSystemContainer() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateSystemContainerName(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantErr   bool
		expectErr error
	}{
		// Valid system names
		{
			name:    "valid system container",
			input:   "_containarium-core",
