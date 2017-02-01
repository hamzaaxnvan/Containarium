package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLabelRequestParsing(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		wantUsername string
		wantLabelKey string
		wantValid    bool
		handlerType  string // "set", "remove", "get"
	}{
		{
			name:         "valid set labels path",
			path:         "/v1/containers/alice/labels",
			wantUsername: "alice",
			wantValid:    true,
			handlerType:  "set",
		},
		{
			name:         "valid get labels path",
			path:         "/v1/containers/bob/labels",
			wantUsername: "bob",
			wantValid:    true,
			handlerType:  "get",
		},
		{
			name:         "valid remove label path",
			path:         "/v1/containers/charlie/labels/team",
			wantUsername: "charlie",
			wantLabelKey: "team",
			wantValid:    true,
			handlerType:  "remove",
		},
		{
			name:         "username with hyphen",
			path:         "/v1/containers/alice-dev/labels",
			wantUsername: "alice-dev",
			wantValid:    true,
			handlerType:  "get",
		},
		{
			name:         "invalid path - no labels suffix",
			path:         "/v1/containers/alice",
			wantUsername: "",
			wantValid:    false,
			handlerType:  "get",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := strings.TrimPrefix(tt.path, "/v1/containers/")
			parts := strings.Split(path, "/")

			var username, labelKey string
			valid := false

			switch tt.handlerType {
			case "set", "get":
				if len(parts) >= 2 && parts[1] == "labels" {
					username = parts[0]
					valid = true
				}
			case "remove":
				if len(parts) >= 3 && parts[1] == "labels" {
					username = parts[0]
					labelKey = parts[2]
					valid = true
				}
			}

			if valid != tt.wantValid {
				t.Errorf("path parsing valid = %v, want %v", valid, tt.wantValid)
			}
			if username != tt.wantUsername {
				t.Errorf("username = %q, want %q", username, tt.wantUsername)
			}
			if labelKey != tt.wantLabelKey {
				t.Errorf("labelKey = %q, want %q", labelKey, tt.wantLabelKey)
			}
		})
	}
}

func TestSetLabelsRequestBody(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantLabels map[string]string
		wantErr    bool
	}{
		{
			name: "valid labels",
			body: `{"labels": {"team": "backend", "env": "prod"}}`,
			wantLabels: map[string]string{
				"team": "backend",
				"env":  "prod",
			},
			wantErr: false,
		},
		{
			name:       "empty labels",
			body:       `{"labels": {}}`,
			wantLabels: map[string]string{},
			wantErr:    false,
		},
		{
			name:       "invalid JSON",
			body:       `{invalid}`,
			wantLabels: nil,
			wantErr:    true,
		},
		{
			name:       "missing labels field",
			body:       `{}`,
			wantLabels: nil,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req SetLabelsRequest
			err := json.NewDecoder(bytes.NewBufferString(tt.body)).Decode(&req)

			if (err != nil) != tt.wantErr {
				t.Errorf("decode error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err == nil && !tt.wantErr {
				if len(req.Labels) != len(tt.wantLabels) {
