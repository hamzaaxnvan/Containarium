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
    '{"ANTHROPIC_API_KEY":"FAKE_ONLY"}',
  );
  return home;
}
const snapshot = (h: string) => stateCommand(h, ["snapshot"]) as Observation;
const begin = (h: string) =>
  (stateCommand(h, ["begin"]) as { generation: number }).generation;
const finish = (h: string, g: number, source: string, outcome: string) =>
  stateCommand(h, ["finish", String(g), source, outcome]);
describe("durable credential episodes", () => {
  it("retains failure/recovery/failure until acknowledgement and deduplicates repeated failures", async () => {
    const home = fixture();
    let failed = true;
    const engine: Engine = {
      name: "claude",
      async run() {
        if (failed) throw new CredentialAuthenticationError("oauth");
        return {
          outputJson: "ok",
          authenticationSucceeded: true,
          authenticationSource: "oauth",
        };
      },
    };
    const run = () =>
      trackClaudeCredentialState(engine, home).run(
        "task",
        cfg,
        new CaptureJournal(),
      );
    await expect(run()).rejects.toThrow("authentication failed");
    await expect(run()).rejects.toThrow();
    const first = snapshot(home).pending[0].id;
    expect(snapshot(home).pending).toHaveLength(1);
    failed = false;
    await run();
    expect(snapshot(home).active).toBe(false);
    expect(snapshot(home).pending[0].id).toBe(first);
    failed = true;
    await expect(run()).rejects.toThrow();
    expect(snapshot(home).pending).toHaveLength(2);
    expect(snapshot(home).pending[1].afterRecovery).toBe(true);
    stateCommand(home, ["ack", first]);
    expect(snapshot(home).pending).toHaveLength(1);
    const last = snapshot(home).pending[0].id;
    stateCommand(home, ["ack", last]);
    expect(snapshot(home).active).toBe(true);
    expect(snapshot(home).pending).toEqual([]);
    await expect(run()).rejects.toThrow();
    expect(snapshot(home).pending).toEqual([]);
    const statePath = join(
      home,
      ".local",
      "state",
      "containarium",
      "claude-auth-state.json",
    );
    expect(statSync(statePath).mode & 0o777).toBe(0o600);
    expect(readFileSync(statePath, "utf8")).not.toContain("FAKE_ONLY");
  });
  it("an old overlapping success cannot recover a newer failure", async () => {
    const home = fixture();
    let resolve!: (v: {
      outputJson: string;
      authenticationSucceeded: boolean;
      authenticationSource: "oauth";
    }) => void;
    const old = trackClaudeCredentialState(
      {
        name: "claude",
        run: () =>
          new Promise((r) => {
            resolve = r;
          }),
      },
      home,
    ).run("old", cfg, new CaptureJournal());
    await expect(
      trackClaudeCredentialState(
        {
          name: "claude",
          async run() {
            throw new CredentialAuthenticationError("oauth");
          },
        },
        home,
      ).run("new", cfg, new CaptureJournal()),
    ).rejects.toThrow();
    resolve({
      outputJson: "ok",
