import {
  CredentialAuthenticationError,
  type AuthenticationSource,
} from "../credential-state.js";
import { query, type SDKMessage } from "@anthropic-ai/claude-agent-sdk";
import type { Engine, EngineConfig, EngineResult } from "../engine.js";
import type { JournalSink } from "../journal.js";
import { describeRunEnding, type EndingField, type RunEnding } from "./ending.js";
import {
  claudeAllowedTools,
  claudeMcpServers,
  mcpServerSpecs,
} from "../mcp.js";

// ClaudeQuery is the shape of the Agent SDK's query(), injectable for tests.
export type ClaudeQuery = (
  params: Parameters<typeof query>[0],
) => AsyncIterable<SDKMessage>;

type ClaudeResult = Extract<SDKMessage, { type: "result" }>;

// ClaudeEngine drives the in-box loop with the Claude Agent SDK (the harness
// that powers Claude Code). It mounts the in-box agent-box binary as an MCP
// server, so agent-box's tools (shell/files/process) are the agent's tool
// surface. Auth: ANTHROPIC_API_KEY from the environment (seeded via secrets).
//
// permissionMode "dontAsk" runs fully non-interactive (deny anything not
// allow-listed, never prompt); allowedTools scopes the agent to agent-box's
// MCP tools (prefix `mcp__<server>__`) and, when the seed carries a
// platform_mcp.json (#1922 D4), exactly the platform tools the daemon
// allow-listed — never the whole platform catalog.
//
// Output schema (#2002): when the skill's agent_card declares an
// output_schema_json, it is passed as the SDK's `outputFormat` (json_schema).
// The SDK validates the agent's final output against the schema and
// re-prompts on mismatch; the artifact is the resulting `structured_output`,
// never the assistant's prose. A run that ends without one — validation
// retries exhausted (error_max_structured_output_retries), any other error
// result, or a "success" that carries no structured_output (the SDK documents
// that case as a failure too) — is rejected by throwing, so the daemon gets
// an error artifact instead of a schema-violating one. Without a declared
// schema the artifact is the concatenated assistant text, as before.
//
// Journal (#2095): every assistant text block, tool_use block and tool_result
// block the query() iterator yields is appended as it arrives; an error
// result is journaled as an error. When the run ends without a final message
// or ends any way but an ordinary end_turn, a status row names the stop
// reason and previews the final message (#2513; see ending.ts).
export class ClaudeEngine implements Engine {
  readonly name = "claude";

  // queryFn is the Agent SDK's query(); a seam for tests.
  constructor(private readonly queryFn: ClaudeQuery = query) {}

  run(
    task: string,
    cfg: EngineConfig,
    journal: JournalSink,
  ): Promise<EngineResult> {
