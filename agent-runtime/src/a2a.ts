import { createHash, timingSafeEqual } from "node:crypto";
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import type { Engine, EngineConfig } from "./engine.js";
import { NULL_JOURNAL, runJournaled, type JournalSink } from "./journal.js";
import type { Seed } from "./seed.js";

// A2A_PORT is the port the daemon resolves for a peer's in-box A2A server
// (resolvePeerA2A in internal/server/agent_server.go uses :8674).
export const A2A_PORT = 8674;

// A2A_TOKEN_ENV carries the per-box secret the daemon derived for THIS box
// (agentA2ASecret in internal/server/a2a_client.go); serveModeCommand exports it
// into the serve-mode process. It is the credential POST /tasks demands.
export const A2A_TOKEN_ENV = "CONTAINARIUM_A2A_TOKEN";

// Wire shapes match the proto (containarium/v1/agent.proto) as serialized by
// protojson, which the daemon's sendA2ATask uses: lowerCamelCase fields, enum
// values as their proto name strings.
interface AgentTaskWire {
  id?: string;
  inputJson?: string;
  input_json?: string; // protojson accepts snake_case on input too
  // The run the task belongs to; names its journal (#2095).
  runId?: string;
  run_id?: string;
}

// TaskJournals resolves a task's run_id to the journal it is written to.
export type TaskJournals = (runId: string | undefined) => JournalSink;
interface AgentArtifactWire {
  taskId: string;
  outputJson: string;
  state: "AGENT_TASK_STATE_COMPLETED" | "AGENT_TASK_STATE_FAILED";
  error: string;
}

// runTask runs one delegated A2A task through the engine and shapes the
// artifact. Pure (engine + cfg injected) — the HTTP layer just adapts to it.
// journals maps the task's run_id to its journal; without it (poll mode) the
// task is not journaled.
export async function runTask(task: AgentTaskWire, engine: Engine, cfg: EngineConfig, journals?: TaskJournals): Promise<AgentArtifactWire> {
  const taskId = task.id ?? "";
  const input = task.inputJson ?? task.input_json ?? "{}";
  try {
    const journal = journals ? journals(task.runId ?? task.run_id) : NULL_JOURNAL;
    const result = await runJournaled(engine, input, cfg, journal);
    return { taskId, outputJson: result.outputJson, state: "AGENT_TASK_STATE_COMPLETED", error: "" };
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e);
    return { taskId, outputJson: "", state: "AGENT_TASK_STATE_FAILED", error: msg };
  }
}

function readBody(req: IncomingMessage): Promise<string> {
  return new Promise((resolve, reject) => {
    let body = "";
    req.on("data", (c) => {
      body += c;
    });
    req.on("end", () => resolve(body));
    req.on("error", reject);
  });
}

