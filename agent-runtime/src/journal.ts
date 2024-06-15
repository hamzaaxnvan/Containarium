import { appendFileSync, existsSync, mkdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import type { Engine, EngineConfig, EngineResult } from "./engine.js";
import type { JournalEvent } from "./journal.schema.js";
import { runWithFinalJsonReprompt } from "./reprompt.js";

// The per-run journal (#2095): an append-only JSON-lines file per run per
// member box at /var/log/agent-runtime/runs/<run_id>/<skill_id>.jsonl, written
// from each engine's message loop so a client can tail what the agent is
// doing by byte offset while it runs. The line format is journal.schema.ts.
// /var/log/agent-runtime.log is untouched: it stays the process log.

export const JOURNAL_ROOT = "/var/log/agent-runtime/runs";

// MAX_FIELD_BYTES caps tool_use.input and tool_result.text (UTF-8 bytes,
// including the truncation marker).
export const MAX_FIELD_BYTES = 2048;
const TRUNCATION_MARKER = "…";

export const REDACTED = "[REDACTED]";
// Secrets shorter than this are ignored: redacting a one-character value
// would shred every line without protecting anything real.
const MIN_SECRET_LENGTH = 8;

// JournalEntry is what an engine appends; the journal assigns seq and t.
export type JournalEntry = JournalEvent extends infer E ? (E extends JournalEvent ? Omit<E, "seq" | "t"> : never) : never;

export interface JournalSink {
  append(entry: JournalEntry): void;
}

// NULL_JOURNAL discards everything: for paths that are not journaled (poll
// mode, a serve-mode task with no run_id).
export const NULL_JOURNAL: JournalSink = { append: () => {} };

// Same shape the daemon enforces on run ids (resolveRunID); skill ids share
// it. Both become path segments, so nothing that could leave the root.
const ID_PATTERN = /^[A-Za-z0-9._-]{1,128}$/;

function checkId(what: string, id: string): void {
  if (!ID_PATTERN.test(id) || id === "." || id === "..") {
    throw new Error(`journal: invalid ${what} ${JSON.stringify(id)} (want 1-128 of [A-Za-z0-9._-])`);
  }
}

// journalPath is <root>/<run_id>/<skill_id>.jsonl. It throws on an id that is
// not a single safe path segment.
export function journalPath(root: string, runId: string, skillId: string): string {
  checkId("run id", runId);
  checkId("skill id", skillId);
  return join(root, runId, `${skillId}.jsonl`);
}

export function redact(s: string, secrets: readonly string[]): string {
  let out = s;
  for (const secret of secrets) {
    if (out.includes(secret)) out = out.split(secret).join(REDACTED);
  }
  return out;
}

// truncateBytes cuts s to at most max UTF-8 bytes (marker included) without
