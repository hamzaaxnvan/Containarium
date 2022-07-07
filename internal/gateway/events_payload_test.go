package gateway

import (
	"encoding/json"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// These payloads were map[string]interface{} literals. web-ui declares the
// matching shape in src/types/events.ts, so the Go side and the TypeScript
// side agreed only by inspection — a mistyped key was valid Go, valid JSON,
// and wrong, with nothing on either side to catch it.
//
// Typing them is only safe if the emitted JSON is unchanged, so that is what
// these assert: the exact key set, and the types the browser will see.

func marshalToMap(t *testing.T, v any) map[string]json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func assertKeys(t *testing.T, got map[string]json.RawMessage, want ...string) {
	t.Helper()
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("payload is missing %q — web-ui's events.ts declares it", k)
		}
	}
	if len(got) != len(want) {
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		t.Errorf("payload has %d keys, want %d: got %v, want %v", len(got), len(want), keys, want)
	}
}

func TestContainerPayloadShape(t *testing.T) {
	got := marshalToMap(t, containerToPayload(&pb.Container{
		Name: "alice-container", Username: "alice",
		State:     pb.ContainerState_CONTAINER_STATE_RUNNING,
		Network:   &pb.NetworkInfo{IpAddress: "10.0.3.5"},
		Resources: &pb.ResourceLimits{Cpu: "2", Memory: "4GB", Disk: "20GB"},
		Image:     "ubuntu:24.04", PodmanEnabled: true, Stack: "nodejs",
	}))
	assertKeys(t, got, "name", "username", "state", "ipAddress", "cpu", "memory", "disk", "image", "podmanEnabled", "stack")
