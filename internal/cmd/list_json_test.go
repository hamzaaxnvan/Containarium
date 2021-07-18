package cmd

import (
	"encoding/json"
	"testing"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// list.go carried its containers as []interface{} and asserted each element
// back to incus.ContainerInfo in three places, and built its --json output
// from map[string]interface{}. Both are the shapes CLAUDE.md names: type
// erasure with no second type behind it, and a wire payload as a map.
//
// The risk in typing them is changing what `--json` emits, so these pin the
// shape rather than the implementation.
func TestListJSONShape(t *testing.T) {
	out := listJSON{
		Containers: []incus.ContainerInfo{{Name: "alice-container", State: "Running"}},
		TotalCount: 1,
	}

	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"containers", "total_count"} {
		if _, ok := got[key]; !ok {
			t.Errorf("`list --json` no longer emits %q — anything parsing this output breaks, "+
				"and a struct-tag typo produces valid JSON with the wrong shape", key)
		}
	}
	if len(got) != 2 {
