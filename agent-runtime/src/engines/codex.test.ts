import type { ThreadEvent } from "@openai/codex-sdk";
import { describe, expect, it } from "vitest";
import type { EngineConfig } from "../engine.js";
import { CaptureJournal } from "../testing/capture.js";
import { CodexEngine } from "./codex.js";

const cfg: EngineConfig = { model: "", systemPrompt: "persona", agentBoxCommand: "agent-box", agentBoxArgs: [], maxTurns: 3 };

function fakeThread(events: ThreadEvent[], seen?: string[], seenOptions?: unknown[]) {
  return () => ({
    async runStreamed(input: string, turnOptions?: unknown) {
      seen?.push(input);
      seenOptions?.push(turnOptions);
      return {
        events: (async function* () {
          for (const e of events) yield e;
        })(),
      };
    },
  });
}

describe("CodexEngine journal", () => {
  it("journals assistant, tool_use and tool_result one line each, in order", async () => {
    const j = new CaptureJournal();
    const seen: string[] = [];
    const engine = new CodexEngine(
      fakeThread(
        [
          { type: "thread.started", thread_id: "th" },
          { type: "turn.started" },
          { type: "item.started", item: { id: "0", type: "agent_message", text: "" } },
          { type: "item.completed", item: { id: "1", type: "agent_message", text: "Looking." } },
          {
            type: "item.completed",
            item: {
              id: "2", type: "mcp_tool_call", server: "agent-box", tool: "shell", arguments: { cmd: "ls" }, status: "completed",
              result: { content: [{ type: "text", text: "README.md" }], structured_content: null },
            },
          },
          { type: "item.completed", item: { id: "3", type: "command_execution", command: "cat x", aggregated_output: "x!", exit_code: 0, status: "completed" } },
          { type: "item.completed", item: { id: "4", type: "agent_message", text: '{"files":1}' } },
          { type: "turn.completed", usage: { input_tokens: 1, cached_input_tokens: 0, output_tokens: 2, reasoning_output_tokens: 0 } },
        ],
        seen,
      ),
    );
    const res = await engine.run("task", cfg, j);
    expect(j.entries).toEqual([
      { kind: "assistant", text: "Looking." },
      { kind: "tool_use", tool: "mcp__agent-box__shell", input: '{"cmd":"ls"}' },
      { kind: "tool_result", tool: "mcp__agent-box__shell", text: "README.md" },
      { kind: "tool_use", tool: "shell", input: "cat x" },
      { kind: "tool_result", tool: "shell", text: "x!" },
      { kind: "assistant", text: '{"files":1}' },
    ]);
    expect(res.outputJson).toBe('{"files":1}');
