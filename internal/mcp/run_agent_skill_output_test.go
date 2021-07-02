package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// runSkillOnlyAPI implements just enough of API to drive handleRunAgentSkill.
// The embedded interface panics if the handler reaches for anything else.
type runSkillOnlyAPI struct {
	API
	resp *RunAgentSkillResponse
}

func (a *runSkillOnlyAPI) RunAgentSkill(RunAgentSkillRequest) (*RunAgentSkillResponse, error) {
	return a.resp, nil
}

// TestHandleRunAgentSkill_Output pins what the run_agent_skill tool prints
// (#2919): a run id is always surfaced with a pointer to crew_logs, a
// server-provided message is surfaced, and the stale "Phase 0 seam" line is
// gone.
