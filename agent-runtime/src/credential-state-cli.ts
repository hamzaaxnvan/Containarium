// Private metadata-only protocol. Linux flock releases the lock on process death.
import {
  closeSync,
  constants,
  existsSync,
  fsyncSync,
  mkdirSync,
  openSync,
  readFileSync,
  realpathSync,
  renameSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { randomUUID } from "node:crypto";
import { homedir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
export interface Episode {
  id: string;
  afterRecovery: boolean;
}
interface State {
  version: 1;
  next: number;
  lastFailure: number;
  lastSuccess: number;
  lateEpisode: string | null;
  recovered: boolean;
  active: string | null;
  pending: Episode[];
}
export interface Observation {
  version: 1;
  active: boolean;
  pending: Episode[];
}
export function stateCommand(home: string, args: string[]): unknown {
  const result = spawnSync(
    process.execPath,
    [
      fileURLToPath(
        new URL("../dist/credential-state-cli.js", import.meta.url),
      ),
      ...args,
    ],
    {
      env: { HOME: home, PATH: process.env.PATH ?? "/usr/bin:/bin" },
      encoding: "utf8",
      maxBuffer: 1 << 20,
      timeout: 15000,
    },
  );
  if (result.error || result.status !== 0)
    throw new Error("credential state unavailable");
