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
