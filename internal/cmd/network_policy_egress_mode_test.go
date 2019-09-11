package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeEgressMode(t *testing.T) {
	for in, want := range map[string]string{
		"":         "",
		"  ":       "",
		"log_only": "NETWORK_POLICY_MODE_LOG_ONLY",
		"log-only": "NETWORK_POLICY_MODE_LOG_ONLY",
		"enforce":  "NETWORK_POLICY_MODE_ENFORCE",
		"ENFORCE":  "NETWORK_POLICY_MODE_ENFORCE",
