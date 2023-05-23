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
				return
			}
			for key, wantValue := range tt.want {
				if gotValue, ok := got[key]; !ok {
					t.Errorf("parseLabelFilter() missing label %q", key)
				} else if gotValue != wantValue {
					t.Errorf("parseLabelFilter() label %q = %q, want %q", key, gotValue, wantValue)
				}
			}
		})
	}
}

func TestFormatLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{
			name:   "nil labels",
			labels: nil,
			want:   "-",
		},
		{
			name:   "empty labels",
			labels: map[string]string{},
			want:   "-",
		},
		{
			name:   "single label",
			labels: map[string]string{"team": "backend"},
			want:   "team=backend",
		},
		// Note: multiple labels order is not guaranteed, so we only test single label for exact match
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatLabels(tt.labels)
			if got != tt.want {
				t.Errorf("formatLabels() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGroupContainersByLabel(t *testing.T) {
	containers := []incus.ContainerInfo{
		{
			Name:   "alice-container",
			State:  "Running",
			Labels: map[string]string{"team": "backend", "env": "prod"},
		},
		{
			Name:   "bob-container",
			State:  "Running",
			Labels: map[string]string{"team": "backend", "env": "staging"},
		},
		{
			Name:   "charlie-container",
			State:  "Stopped",
			Labels: map[string]string{"team": "frontend", "env": "prod"},
		},
		{
			Name:   "dave-container",
			State:  "Running",
			Labels: nil,
		},
		{
