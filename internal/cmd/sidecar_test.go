//go:build !containarium_client

package cmd

import (
	"strings"
	"testing"
)

func TestRenderOTelComposeSnippet_AllFieldsRendered(t *testing.T) {
	got := renderOTelComposeSnippet(otelComposeInputs{
		ImageTag:          "v0.16.11",
		ServiceName:       "alice-otel",
		Username:          "alice",
		ContainerEnvName:  "alice-container",
		BackendEnvValue:   "<your-primary-vm>",
		TenantEnvValue:    "alice",
		CollectorEnvValue: "http://10.0.3.112:4318",
	})

	mustContain := []string{
		// GHCR image tag (public package; tenants `docker pull` directly).
		"image: ghcr.io/footprintai/containarium-otel-sidecar:v0.16.11",
