// Re-prompt once for a missing final message (#2513 item 2).
//
// A skill's contract asks for one final JSON object. A run that edited files
// and then ended its turn without a final message used to fail outright, with
// its work on disk. The maintainer decision (2026-10-25): ask the agent once,
// in the same conversation, for its final JSON. If it still returns none, the
// run fails with that reason. The runtime never builds a result from the
// changed files itself: a synthesised summary would claim things the agent
// never said.
import type { Engine, EngineConfig, EngineResult } from "./engine.js";
import type { JournalEntry, JournalSink } from "./journal.js";
import { AGENT_BOX_MCP_NAME } from "./mcp.js";
import { shellCommandWritesFiles } from "./shell-edit.js";
import { sumUsage } from "./usage.js";

// FINAL_JSON_REPROMPT is the one follow-up message the agent gets.
export const FINAL_JSON_REPROMPT =
  "You changed files but ended your turn without a final message. " +
  "Reply now with only the final JSON object your instructions ask for, with no tool calls and no other text. " +
  "Report only what you actually did.";

// The tools that change files: agent-box's write/move/delete (bare under
// Gemini, mcp__agent-box__<tool> under Claude and Codex) and Codex's native
// file_change item. A tool of any other MCP server is not counted, whatever it
// is called. A shell command counts when shellCommandWritesFiles says it
// writes (shell-edit.ts; #3107).
const FILE_CHANGING_TOOLS: ReadonlySet<string> = new Set(["write_file", "move_file", "delete_file", "file_change"]);
const SHELL_TOOLS: ReadonlySet<string> = new Set(["shell_exec", "shell"]);
const AGENT_BOX_PREFIX = `mcp__${AGENT_BOX_MCP_NAME}__`;

// agentBoxToolName is the tool's bare name when it is agent-box's (or an
// engine-native tool journaled bare), or undefined for another server's tool.
function agentBoxToolName(tool: string): string | undefined {
  if (tool.startsWith(AGENT_BOX_PREFIX)) return tool.slice(AGENT_BOX_PREFIX.length);
  return tool.startsWith("mcp__") ? undefined : tool;
}

// isFileChangingTool reports whether a journaled tool name changes files.
export function isFileChangingTool(tool: string): boolean {
  const bare = agentBoxToolName(tool);
  return bare !== undefined && FILE_CHANGING_TOOLS.has(bare);
}

// isFileChangingToolUse is isFileChangingTool plus the shell tools, whose
// input says whether the command writes. shell_exec's input is the journaled
// JSON arguments; Codex's shell input is the raw command.
export function isFileChangingToolUse(tool: string, input: string): boolean {
  if (isFileChangingTool(tool)) return true;
  const bare = agentBoxToolName(tool);
  if (bare === undefined || !SHELL_TOOLS.has(bare)) return false;
  return shellCommandWritesFiles(bare === "shell" ? input : shellExecCommand(input));
}

function shellExecCommand(input: string): string {
  try {
