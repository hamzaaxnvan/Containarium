package cmd

import (
	"encoding/json"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
)

func TestParseLabelFilter(t *testing.T) {
	tests := []struct {
		name       string
		labelSlice []string
		want       map[string]string
	}{
		{
			name:       "empty slice",
			labelSlice: []string{},
			want:       map[string]string{},
		},
		{
			name:       "single label",
			labelSlice: []string{"team=backend"},
			want:       map[string]string{"team": "backend"},
		},
		{
			name:       "multiple labels",
			labelSlice: []string{"team=backend", "env=prod", "owner=alice"},
			want: map[string]string{
				"team":  "backend",
				"env":   "prod",
				"owner": "alice",
			},
		},
		{
			name:       "label with spaces",
			labelSlice: []string{" team = backend "},
			want:       map[string]string{"team": "backend"},
		},
		{
			name:       "label with value containing equals",
			labelSlice: []string{"config=key=value"},
			want:       map[string]string{"config": "key=value"},
		},
		{
			name:       "invalid label format (no equals)",
			labelSlice: []string{"invalid"},
			want:       map[string]string{},
		},
		{
			name:       "empty key",
			labelSlice: []string{"=value"},
			want:       map[string]string{},
		},
		{
			name:       "mixed valid and invalid",
			labelSlice: []string{"team=dev", "invalid", "env=staging"},
			want: map[string]string{
				"team": "dev",
				"env":  "staging",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLabelFilter(tt.labelSlice)
			if len(got) != len(tt.want) {
				t.Errorf("parseLabelFilter() returned %d labels, want %d", len(got), len(tt.want))
