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
