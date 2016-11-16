import { existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import type { Engine, EngineConfig } from "./engine.js";
import { runOnce } from "./run.js";

const cfg: EngineConfig = { model: "", systemPrompt: "", agentBoxCommand: "agent-box", agentBoxArgs: [], maxTurns: 1 };

function dirs(): { seedDir: string; root: string } {
  return { seedDir: mkdtempSync(join(tmpdir(), "seed-")), root: mkdtempSync(join(tmpdir(), "journal-")) };
}

const okEngine: Engine = {
  name: "fake",
  async run(_task, _cfg, journal) {
    journal.append({ kind: "assistant", text: "hello" });
    return { outputJson: '{"ok":true}' };
  },
};

function artifact(seedDir: string): { outputJson: string; error?: string } {
  return JSON.parse(readFileSync(join(seedDir, "artifact.json"), "utf8")) as { outputJson: string; error?: string };
}

describe("runOnce (run mode)", () => {
  it("records both turns' summed usage in artifact.json after a re-prompt (#3107)", async () => {
    const { seedDir, root } = dirs();
    const engine: Engine = {
      name: "fake",
      async run(_t, _c, journal) {
        journal.append({ kind: "tool_use", tool: "write_file", input: "{}" });
        return {
          outputJson: "",
          usage: { output_tokens: 7 },
          followUp: async () => ({ outputJson: '{"ok":true}', usage: { output_tokens: 5 } }),
        };
      },
    };
    const code = await runOnce({
      env: { CONTAINARIUM_RUN_ID: "run-u", CONTAINARIUM_SKILL_ID: "s" },
