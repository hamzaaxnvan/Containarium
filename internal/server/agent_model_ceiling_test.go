package server

import (
	"context"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/agentengine"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/modelgateway"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/skills"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// modelCeilingSkillYAML is a minimal named-engine, named-model skill — the
// embedded catalog's own skills (hello-agent-claude/-codex, #2225) deliberately
// pin no model, so #2229's check needs a fixture that does.
const modelCeilingSkillYAML = `
skills:
  - id: model-ceiling-test
    name: Model Ceiling Test
    recipe_id: agent-runtime
    system_prompt: test skill for #2229's model-ceiling check.
    allowed_scopes:
      - containers:read
    engine: codex
    model: gpt-5-codex
`

// fakeModelLister is a modelLister test double with a call recorder (the
// issue's own AC: "the model list lookup is a fake with a call recorder").
type fakeModelLister struct {
	models []string
	err    error
	calls  []struct{ keyOwner, provider string }
}

func (f *fakeModelLister) ListModels(_ context.Context, keyOwner, provider string) ([]string, error) {
	f.calls = append(f.calls, struct{ keyOwner, provider string }{keyOwner, provider})
	if f.err != nil {
		return nil, f.err
	}
	return f.models, nil
}

// newModelCeilingHarness is newSkillBoxHarness, generalized to a caller-
// supplied catalog and modelLister — #2229's model-ceiling check needs a
