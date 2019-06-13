package containariumotel

import (
	"strings"
	"testing"
)

func TestPrintConfig_Empty(t *testing.T) {
	var sb strings.Builder
	err := printConfig(&sb, DistroConfig{}, true)
	if err != nil {
		t.Fatalf("printConfig: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "TELEMETRY WILL BE NO-OP") {
		t.Errorf("missing no-op status; output:\n%s", out)
	}
	if !strings.Contains(out, "containarium.distro") {
		t.Errorf("missing distro stamp; output:\n%s", out)
	}
}

func TestPrintConfig_Populated(t *testing.T) {
	var sb strings.Builder
	err := printConfig(&sb, DistroConfig{
		Endpoint:       "http://10.0.3.42:4318",
		ServiceName:    "payment-api",
		ContainerID:    "alice-container",
		BackendID:      "node-7",
		TenantID:       "alice",
		ServiceVersion: "v1.2.3",
	}, true)
	if err != nil {
		t.Fatalf("printConfig: %v", err)
	}
	out := sb.String()
	for _, want := range []string{
		"10.0.3.42:4318",
		"payment-api",
		"alice-container",
		"node-7",
		"v1.2.3",
