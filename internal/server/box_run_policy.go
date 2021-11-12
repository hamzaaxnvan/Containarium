package server

import (
	"fmt"
	"strings"

	"github.com/footprintai/containarium/internal/toolaccess"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// What an unprivileged platform-started run may do, per recorded tool-access
// level (#2944, slice 2). It is expressed as flags on the run command itself:
// the daemon builds that command as root, so the agent cannot change its own
// flags, and `--restricted` makes the agent CLI ignore every settings file except
// `--settings` and managed ones, so neither the run user's home nor the cloned
// repository can add allow rules or hooks of its own. Nothing is written to
// /etc and nothing the box owner sees (their own sessions) is affected.
//
// The flags are specific to the Claude Code CLI; any other engine is refused
// rather than started without a policy.

// boxRunSkillFetchTool is the agent CLI's permission name for the one tool of the
// box's skills MCP server a skill-aware run is told to call. Read-only.
const boxRunSkillFetchTool = "mcp__containarium-skills__get_skill_content"

// boxRunPolicy is the command-line policy for one level.
type boxRunPolicy struct {
	// Tools are the built-in tools the run may use at all (--tools).
	Tools []string
	// Allowed are the permission rules that need no approval (--allowedTools).
	Allowed []string
}

// bashRules spells each command prefix both bare and with arguments, because
// the CLI matches `Bash(git status)` and `Bash(git status *)` separately.
func bashRules(prefixes ...string) []string {
	rules := make([]string, 0, 2*len(prefixes))
	for _, p := range prefixes {
		rules = append(rules, "Bash("+p+")", "Bash("+p+" *)")
	}
