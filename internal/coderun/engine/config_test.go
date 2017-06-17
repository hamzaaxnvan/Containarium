package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCodeConfigRoundTrip is the AC's round-trip test for the record `code
// install` leaves on the box and `code run` reads back.
func TestCodeConfigRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		cfg  CodeConfig
	}{
		{
			name: "pi on the gateway",
			cfg: CodeConfig{
				Version:    CodeConfigVersion,
				Engine:     NamePi,
				Credential: KindGateway,
				Provider:   "kafeido",
				Model:      "kafeido-coder",
			},
		},
		{
			name: "pi on a tenant secret",
			cfg: CodeConfig{
				Version:    CodeConfigVersion,
				Engine:     NamePi,
				Credential: KindSecret,
				SecretName: "ANTHROPIC_API_KEY",
				Model:      "claude-sonnet-5",
			},
		},
		{
			name: "claude, the default",
			cfg: CodeConfig{
				Version:    CodeConfigVersion,
				Engine:     NameClaude,
				Credential: KindSecret,
			},
		},
	}

	for _, tc := range tests {
