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
