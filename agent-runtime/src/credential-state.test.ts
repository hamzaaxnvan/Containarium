import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import {
  CredentialAuthenticationError,
  trackClaudeCredentialState,
} from "./credential-state.js";
import { stateCommand, type Observation } from "./credential-state-cli.js";
import type { Engine, EngineConfig } from "./engine.js";
import { CaptureJournal } from "./testing/capture.js";
const cfg: EngineConfig = {
  model: "",
  systemPrompt: "",
  agentBoxCommand: "fake",
  agentBoxArgs: [],
  maxTurns: 1,
};
const homes: string[] = [];
afterEach(() => {
  for (const h of homes.splice(0)) rmSync(h, { recursive: true, force: true });
});
function fixture() {
  const home = mkdtempSync(join(tmpdir(), "auth-episodes-"));
  homes.push(home);
  mkdirSync(join(home, ".claude"));
  writeFileSync(
    join(home, ".claude", ".credentials.json"),
    '{"accessToken":"FAKE_ONLY","expiresAt":9999999999999}',
  );
  writeFileSync(
    join(home, ".claude", "settings.json"),
