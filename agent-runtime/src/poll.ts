import { runTask } from "./a2a.js";
import type { Engine, EngineConfig } from "./engine.js";

// poll.ts — worker side of the pull-based run queue (prototype). Instead of the
// daemon exec-ing a run into the box (push), a long-lived worker box LEASES
// tasks from the daemon/gateway over HTTP, runs them locally through the same
// engine, and reports the result back. All traffic is OUTBOUND from the box, so
// it works behind NAT / a tunnel with no inbound listener.
// See docs/AGENT-MODEL-GATEWAY-DESIGN.md (pull-queue section).

// Wire shapes match protojson (lowerCamelCase) on the agent-tasks endpoints.
interface LeaseResponseWire {
  hasTask?: boolean;
  taskId?: string;
  skillId?: string;
  inputJson?: string;
  leaseToken?: string;
}

export interface PollConfig {
  // Base URL of the daemon/gateway, e.g. "https://<host>:8080".
  baseUrl: string;
  // Bearer token authorizing the queue calls (needs agents:run). This is the
  // worker's *runtime* credential, distinct from the skill's in-box token —
  // see the design note's open question on who mints it.
  token: string;
  workerId: string;
  // Lease only this skill's tasks; "" = any skill.
  skillId: string;
  // Visibility timeout requested per lease; keep > worst-case run time.
  leaseSeconds: number;
  // Backoff when the queue is empty.
  idleMs: number;
  // When true, process exactly one task then return (live-test / one-shot).
  once: boolean;
}

const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

async function postJSON(url: string, token: string, body: unknown): Promise<Response> {
  return fetch(url, {
    method: "POST",
    headers: { "content-type": "application/json", authorization: `Bearer ${token}` },
    body: JSON.stringify(body),
  });
}

async function lease(cfg: PollConfig): Promise<LeaseResponseWire> {
  const res = await postJSON(`${cfg.baseUrl}/v1/agent-tasks/lease`, cfg.token, {
    workerId: cfg.workerId,
    skillId: cfg.skillId,
    leaseSeconds: cfg.leaseSeconds,
  });
  if (!res.ok) throw new Error(`lease failed: HTTP ${res.status} ${await res.text()}`);
  return (await res.json()) as LeaseResponseWire;
}

async function complete(
  cfg: PollConfig,
  taskId: string,
  leaseToken: string,
  artifactJson: string,
  error: string,
): Promise<boolean> {
  const res = await postJSON(
    `${cfg.baseUrl}/v1/agent-tasks/${encodeURIComponent(taskId)}/complete`,
