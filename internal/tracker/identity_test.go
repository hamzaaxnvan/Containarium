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

func TestStamp_OmitsModelWhenManifestHasNone(t *testing.T) {
	got := Stamp(Identity{RunID: "run-1", SkillID: "skill-1"}, KindClaim)
	if strings.Contains(got, "()") {
		t.Errorf("Stamp = %q, want no empty parens when Model is unset", got)
	}
	if !strings.HasPrefix(got, "— skill-1/run-1 via Containarium\n") {
		t.Errorf("Stamp = %q, want the visible line with no model segment", got)
	}
}

func TestStamp_TruncatesLongRunIDInVisibleLineOnly(t *testing.T) {
	longRunID := "run-0123456789abcdefextra"
	got := Stamp(Identity{RunID: longRunID, SkillID: "s"}, KindComment)
	lines := strings.SplitN(got, "\n", 2)
	if strings.Contains(lines[0], longRunID) {
		t.Errorf("visible line %q contains the full run id, want it truncated", lines[0])
	}
	// The marker (used for machine parsing / audit) must still carry
	// the FULL run id — only the human-facing line is shortened.
	runID, _, _, ok := ParseMarker(got)
	if !ok || runID != longRunID {
		t.Errorf("ParseMarker run id = %q, ok=%v, want the untruncated %q", runID, ok, longRunID)
	}
}

func TestParseMarker_NoMarkerPresent(t *testing.T) {
	if _, _, _, ok := ParseMarker("just a plain comment, nothing hidden here"); ok {
		t.Error("ParseMarker found a marker in plain text")
	}
}
