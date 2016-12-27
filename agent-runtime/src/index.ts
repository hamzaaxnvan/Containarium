import { trackClaudeCredentialState } from "./credential-state.js";
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { A2A_TOKEN_ENV, startA2AServer } from "./a2a.js";
import type { Engine, EngineConfig } from "./engine.js";
import { ClaudeEngine } from "./engines/claude.js";
import { CodexEngine } from "./engines/codex.js";
import { GeminiEngine } from "./engines/gemini.js";
import { journalSecrets, serveJournals } from "./journal.js";
import { codexConfigToml } from "./mcp.js";
import { pollConfigFromEnv, runPollLoop } from "./poll.js";
import { runOnce } from "./run.js";
import { DEFAULT_SEED_DIR, loadSeed } from "./seed.js";

// The in-box loop entrypoint (Phase 4a). Reads the seed the daemon planted,
// runs one task to completion through the selected engine (Claude Agent SDK,
// OpenAI Codex SDK, or Google Gen AI SDK) — all mounting agent-box as their MCP
// tool surface — and writes artifact.json back for the daemon to return.
//
// Engine selection: CONTAINARIUM_AGENT_ENGINE (claude | codex | gemini), default
// claude. (A later phase moves this onto the skill manifest as an `engine` field.)

const seedDir = process.env.AGENT_SEED_DIR ?? DEFAULT_SEED_DIR;
const engineName = (process.env.CONTAINARIUM_AGENT_ENGINE ?? "claude").toLowerCase();
const agentBoxCommand = process.env.AGENT_BOX_BIN ?? "agent-box";
const maxTurns = Number(process.env.CONTAINARIUM_AGENT_MAX_TURNS ?? "12");

// Model default is engine-specific. Claude → claude-opus-4-8 (per the Claude
// API guidance). Gemini → gemini-2.5-flash (so the effective model is recorded
// in the artifact for audit, not just defaulted inside the engine). Codex →
// empty, i.e. let Codex use its own configured default (we don't hard-code an
// OpenAI model id). Override any with CONTAINARIUM_AGENT_MODEL.
const engineModelDefaults: Record<string, string> = {
  claude: "claude-opus-4-8",
  gemini: "gemini-2.5-flash",
};
const model = process.env.CONTAINARIUM_AGENT_MODEL ?? (engineModelDefaults[engineName] ?? "");

function pickEngine(name: string): Engine {
  switch (name) {
    case "claude":
      return new ClaudeEngine();
    case "codex":
      return new CodexEngine();
    case "gemini":
      return new GeminiEngine();
    default:
      throw new Error(`unknown engine ${name} (want: claude | codex | gemini)`);
  }
}

// writeCodexConfig registers the MCP servers (agent-box, plus the platform MCP
// when the seed carries one) and the model, if set, in ~/.codex/config.toml,
// which the Codex CLI the SDK drives reads. The Claude engine takes its MCP
// config inline, so this is Codex-only.
function writeCodexConfig(cfg: EngineConfig): void {
  const dir = join(homedir(), ".codex");
  if (!existsSync(dir)) mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, "config.toml"), codexConfigToml(cfg));
}

// mode: "run" (one-shot — read input.json, run once, write artifact.json; the
