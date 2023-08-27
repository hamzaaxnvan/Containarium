package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetStringArg tests the getStringArg helper
func TestGetStringArg(t *testing.T) {
	tests := []struct {
		name         string
		args         map[string]interface{}
		key          string
		defaultValue string
		expected     string
	}{
		{
			name:         "key exists with string value",
			args:         map[string]interface{}{"cpu": "4"},
			key:          "cpu",
			defaultValue: "2",
			expected:     "4",
		},
		{
			name:         "key missing returns default",
			args:         map[string]interface{}{},
			key:          "cpu",
			defaultValue: "2",
			expected:     "2",
		},
		{
			name:         "key exists with empty string returns default",
			args:         map[string]interface{}{"cpu": ""},
			key:          "cpu",
			defaultValue: "2",
			expected:     "2",
		},
		{
			name:         "key exists with wrong type returns default",
			args:         map[string]interface{}{"cpu": 123},
			key:          "cpu",
			defaultValue: "2",
			expected:     "2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getStringArg(tt.args, tt.key, tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestGetBoolArg tests the getBoolArg helper
func TestGetBoolArg(t *testing.T) {
	tests := []struct {
		name         string
		args         map[string]interface{}
		key          string
		defaultValue bool
		expected     bool
	}{
		{
			name:         "key exists with true",
			args:         map[string]interface{}{"force": true},
			key:          "force",
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "key exists with false",
			args:         map[string]interface{}{"force": false},
			key:          "force",
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "key missing returns default",
			args:         map[string]interface{}{},
			key:          "force",
			defaultValue: true,
			expected:     true,
		},
		{
			name:         "key exists with wrong type returns default",
			args:         map[string]interface{}{"force": "yes"},
			key:          "force",
			defaultValue: false,
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getBoolArg(tt.args, tt.key, tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestToolInputSchemaStructure tests that all tool schemas are well-formed
func TestToolInputSchemaStructure(t *testing.T) {
	config := &Config{
		ServerURL: "http://localhost:8080",
		JWTToken:  "test-token",
	}

	server, err := NewServer(config)
	require.NoError(t, err)

	for _, tool := range server.tools {
		t.Run(tool.Name, func(t *testing.T) {
			schema := tool.InputSchema

			// Check required fields
			assert.Equal(t, "object", schema["type"])
			assert.NotNil(t, schema["properties"])

			// Validate properties structure
			properties, ok := schema["properties"].(map[string]interface{})
			require.True(t, ok, "properties should be a map")

			// Each property should have type and description
			for propName, propValue := range properties {
				prop, ok := propValue.(map[string]interface{})
				require.True(t, ok, "property %s should be a map", propName)

				assert.NotEmpty(t, prop["type"], "property %s should have type", propName)
				assert.NotEmpty(t, prop["description"], "property %s should have description", propName)
			}

			// Check required array if present
			if required, ok := schema["required"].([]string); ok {
				// All required fields should exist in properties
				for _, req := range required {
					assert.Contains(t, properties, req, "required field %s should exist in properties", req)
				}
			}
		})
	}
}

// TestCreateContainerToolSchema tests create_container tool schema
func TestCreateContainerToolSchema(t *testing.T) {
	config := &Config{
		ServerURL: "http://localhost:8080",
		JWTToken:  "test-token",
	}

	server, err := NewServer(config)
