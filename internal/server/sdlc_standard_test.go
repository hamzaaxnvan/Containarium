package server

import (
	"errors"
	"strings"
	"testing"

	"github.com/footprintai/containarium/pkg/core/skills"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #2488: CONTAINARIUM_SDLC_SKILLS_BUNDLE points the daemon at the pinned
// sdlc-skills.tar.gz. Every way that can go wrong ends with the inline prompt
// and a log line saying why; nothing is half-applied.

const stdYAML = `skills:
  - id: hosted
    name: Hosted
    recipe_id: agent-runtime
    system_prompt: contract text
    allowed_scopes: [containers:read]
    standard_from: engineer-implement
    standard_section: Engineering principles
`

type stubStd struct{ sec string }

