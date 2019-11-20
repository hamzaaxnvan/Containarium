package incus

import (
	"testing"
)

func TestExtractLabelsFromConfig(t *testing.T) {
	tests := []struct {
		name       string
		config     map[string]string
		wantLabels map[string]string
	}{
		{
			name:       "empty config",
			config:     map[string]string{},
			wantLabels: map[string]string{},
		},
		{
			name: "config without labels",
			config: map[string]string{
				"limits.cpu":    "4",
				"limits.memory": "8GB",
			},
			wantLabels: map[string]string{},
		},
		{
			name: "config with single label",
			config: map[string]string{
				"limits.cpu":                   "4",
				"user.containarium.label.team": "backend",
			},
			wantLabels: map[string]string{
				"team": "backend",
			},
		},
		{
			name: "config with multiple labels",
			config: map[string]string{
				"limits.cpu":                      "4",
				"user.containarium.label.team":    "backend",
				"user.containarium.label.project": "api",
				"user.containarium.label.env":     "production",
				"user.containarium.label.owner":   "alice",
			},
			wantLabels: map[string]string{
				"team":    "backend",
				"project": "api",
				"env":     "production",
				"owner":   "alice",
			},
		},
		{
			name: "label with special characters in value",
			config: map[string]string{
				"user.containarium.label.description": "Production API Server v2.0",
				"user.containarium.label.url":         "https://example.com/api",
			},
			wantLabels: map[string]string{
				"description": "Production API Server v2.0",
				"url":         "https://example.com/api",
			},
		},
		{
			name: "label with empty value",
			config: map[string]string{
				"user.containarium.label.empty": "",
			},
			wantLabels: map[string]string{
				"empty": "",
			},
		},
		{
			name: "similar prefix but not a label",
			config: map[string]string{
				"containarium.labels":          "not a label",
				"user.containarium.label.real": "label",
				"containarium.labelconfig":     "also not a label",
			},
			wantLabels: map[string]string{
				"real": "label",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLabelsFromConfig(tt.config)

			if len(got) != len(tt.wantLabels) {
				t.Errorf("extractLabelsFromConfig() returned %d labels, want %d", len(got), len(tt.wantLabels))
				return
			}

			for key, wantValue := range tt.wantLabels {
				if gotValue, ok := got[key]; !ok {
					t.Errorf("extractLabelsFromConfig() missing label %q", key)
