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
		{
			name:      "cap-plus-one-second is rejected",
			input:     "168h1s",
			wantErr:   true,
			errSubstr: "exceeds maximum",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTTL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseTTL(%q) = %v, nil; want error containing %q", tt.input, got, tt.errSubstr)
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("parseTTL(%q) error = %q; want substring %q", tt.input, err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTTL(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("parseTTL(%q) = %v; want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseTTL_MaxCapMatchesDocumentation(t *testing.T) {
	// Guards against accidental edits to the cap constant. The help
	// text and PR description promise 168h (7 days); if someone bumps
	// the constant they should bump this test (and the docs) too.
	if maxTTL != 168*time.Hour {
		t.Errorf("maxTTL = %v; want 168h — docs and help text reference 7 days", maxTTL)
	}
}

func TestIsUnimplemented(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil is not unimplemented",
			err:  nil,
			want: false,
		},
		{
			name: "plain error is not unimplemented",
			err:  errors.New("boom"),
			want: false,
		},
		{
			name: "gRPC Unimplemented is detected",
			err:  status.Errorf(codes.Unimplemented, "nope"),
			want: true,
		},
		{
			name: "gRPC NotFound is not Unimplemented",
			err:  status.Errorf(codes.NotFound, "missing"),
			want: false,
		},
		{
			name: "gRPC InvalidArgument is not Unimplemented",
			err:  status.Errorf(codes.InvalidArgument, "bad"),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUnimplemented(tt.err); got != tt.want {
				t.Errorf("isUnimplemented(%v) = %v; want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsAlreadyExists(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil is not AlreadyExists", nil, false},
		{"plain error is not AlreadyExists", errors.New("boom"), false},
		{"gRPC AlreadyExists is detected", status.Errorf(codes.AlreadyExists, "dup"), true},
		{"gRPC Unimplemented is not AlreadyExists", status.Errorf(codes.Unimplemented, "nope"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAlreadyExists(tt.err); got != tt.want {
				t.Errorf("isAlreadyExists(%v) = %v; want %v", tt.err, got, tt.want)
			}
		})
	}
}
