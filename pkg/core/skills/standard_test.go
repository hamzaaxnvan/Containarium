package skills

import (
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #2488: a skill can declare that its system prompt is composed from the
// catalog's inline text plus one section of a pinned open-sdlc-skills skill.
// Without a configured bundle the prompt is the inline text, byte for byte.

const standardYAML = `skills:
  - id: hosted-engineer
    name: Hosted engineer
    recipe_id: agent-runtime
    system_prompt: >-
      You implement issues. Your FINAL message is exactly one JSON object.
    allowed_scopes: [containers:read]
    standard_from: engineer-implement
    standard_section: Engineering principles
  - id: plain
    name: Plain
    recipe_id: agent-runtime
    system_prompt: hello
    allowed_scopes: [containers:read]
`

type fakeStandard struct {
	sections map[string]string // "skill/section" -> text
	pin      string
}

func (f fakeStandard) Section(skill, section string) (string, bool) {
	t, ok := f.sections[skill+"/"+section]
	return t, ok
}
func (f fakeStandard) Pin() string { return f.pin }

func loadStandardCatalog(t *testing.T) *Manager {
	t.Helper()
	m := New()
