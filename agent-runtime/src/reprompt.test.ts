import { describe, expect, it } from "vitest";
import type { Engine, EngineConfig, EngineResult } from "./engine.js";
import type { JournalSink } from "./journal.js";
import { FINAL_JSON_REPROMPT, isFileChangingTool, isFileChangingToolUse, runWithFinalJsonReprompt } from "./reprompt.js";
import { CaptureJournal } from "./testing/capture.js";

const cfg: EngineConfig = { model: "", systemPrompt: "", agentBoxCommand: "agent-box", agentBoxArgs: [], maxTurns: 1 };

// scriptedEngine plays one first turn and, if the engine supports follow-ups,
// one reply per follow-up from `replies` (#2513 item 2). `prompts` records
// each follow-up prompt it was sent.
function scriptedEngine(
  first: (j: JournalSink) => string,
  replies: string[] | undefined,
  prompts: string[] = [],
): Engine {
  const reply = async (prompt: string, j: JournalSink): Promise<EngineResult> => {
    prompts.push(prompt);
    const out = replies?.shift() ?? "";
    if (out) j.append({ kind: "assistant", text: out });
    return { outputJson: out, ...(replies ? { followUp: reply } : {}) };
  };
  return {
    name: "scripted",
    async run(_task, _cfg, journal) {
      return { outputJson: first(journal), ...(replies ? { followUp: reply } : {}) };
    },
  };
}

const editsFile = (tool: string) => (j: JournalSink) => {
  j.append({ kind: "tool_use", tool, input: '{"path":"text/slug.go"}' });
  j.append({ kind: "tool_result", tool, text: "bytes_written: 649" });
  return "";
};

describe("isFileChangingTool", () => {
  it.each([
    ["write_file", true],
    ["move_file", true],
    ["delete_file", true],
    ["mcp__agent-box__write_file", true],
    ["mcp__agent-box__delete_file", true],
    ["file_change", true],
    ["read_file", false],
    ["list_directory", false],
    ["shell_exec", false],
    ["mcp__agent-box__read_file", false],
    // #3107 item 5: only agent-box's tools count under the mcp__ prefix.
    ["mcp__other__write_file", false],
    ["mcp__containarium__delete_file", false],
    ["mcp__agent-box-evil__write_file", false],
