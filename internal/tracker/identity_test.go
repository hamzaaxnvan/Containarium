package tracker

import (
	"strings"
	"testing"
)

func TestStamp_FromClaimsOnly(t *testing.T) {
	// Stamp's signature is a pure function of Identity — there is no
	// second parameter a caller could use to influence it, so the type
	// system itself is the proof: nothing but the verified Identity
	// (built by the caller from JWT claims + the run record, never from
	// request fields) can appear in the output. This test pins the
	// exact rendering so a future refactor can't quietly reopen an
	// injection point in the string-building itself.
	id := Identity{RunID: "run-abcdef123456789", SkillID: "code-review", Model: "sonnet"}
	got := Stamp(id, KindComment)

	if !strings.HasPrefix(got, "— code-review/run-abcdef12 (sonnet) via Containarium\n") {
		t.Fatalf("Stamp visible line = %q", got)
	}
	runID, skillID, kind, ok := ParseMarker(got)
	if !ok {
		t.Fatal("ParseMarker found no marker in Stamp's own output")
	}
	if runID != id.RunID || skillID != id.SkillID || kind != KindComment {
		t.Errorf("ParseMarker = (%q, %q, %q), want (%q, %q, %q)", runID, skillID, kind, id.RunID, id.SkillID, KindComment)
	}
}

