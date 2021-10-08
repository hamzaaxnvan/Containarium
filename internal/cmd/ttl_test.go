package cmd

import (
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestParseTTL(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      time.Duration
		wantErr   bool
		errSubstr string
	}{
		{
			name:    "valid 30 minutes",
			input:   "30m",
			want:    30 * time.Minute,
			wantErr: false,
		},
		{
			name:    "valid 1 hour",
			input:   "1h",
			want:    time.Hour,
			wantErr: false,
		},
		{
			name:    "valid 24 hours",
			input:   "24h",
			want:    24 * time.Hour,
			wantErr: false,
		},
		{
			name:    "valid at exactly the cap",
			input:   "168h",
			want:    168 * time.Hour,
			wantErr: false,
		},
		{
			name:    "valid mixed units",
			input:   "1h30m",
			want:    time.Hour + 30*time.Minute,
			wantErr: false,
		},
		{
			name:    "valid seconds",
			input:   "45s",
			want:    45 * time.Second,
			wantErr: false,
		},
		{
			name:      "empty string is rejected",
			input:     "",
			wantErr:   true,
			errSubstr: "required",
		},
		{
			name:      "garbage string is rejected",
			input:     "not-a-duration",
			wantErr:   true,
			errSubstr: "invalid duration",
		},
		{
			name:      "bare number is rejected (no units)",
			input:     "30",
			wantErr:   true,
			errSubstr: "invalid duration",
		},
		{
			name:      "zero duration is rejected",
			input:     "0s",
			wantErr:   true,
			errSubstr: "must be positive",
		},
		{
			name:      "negative duration is rejected",
			input:     "-1h",
			wantErr:   true,
			errSubstr: "must be positive",
		},
		{
			name:      "just over the cap is rejected",
			input:     "169h",
			wantErr:   true,
			errSubstr: "exceeds maximum",
		},
		{
			name:      "way over the cap (30 days) is rejected",
			input:     "720h",
			wantErr:   true,
			errSubstr: "exceeds maximum",
		},
