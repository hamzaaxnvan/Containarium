package containariumotel

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
)

func TestBuildResource_ContainariumAttrs(t *testing.T) {
	clearEnv(t)
	ctx := context.Background()
	cfg := DistroConfig{
		ContainerID:    "alice-container",
		BackendID:      "node-7",
		TenantID:       "alice",
		ServiceVersion: "v1.2.3",
	}
	res, err := buildResource(ctx, cfg, nil, "0.20.0")
	if err != nil {
		t.Fatalf("buildResource: %v", err)
	}
	want := map[string]string{
		"container.id":        "alice-container",
		"backend.id":          "node-7",
		"service.namespace":   "alice",
		"service.version":     "v1.2.3",
		"containarium.distro": "go/0.20.0",
	}
	for k, v := range want {
		got := attrValueString(res, k)
		if got != v {
			t.Errorf("attr %q = %q, want %q", k, got, v)
		}
	}
}

func TestBuildResource_DistroStampDefended(t *testing.T) {
	clearEnv(t)
	ctx := context.Background()
