import { Codex, type Thread, type ThreadItem } from "@openai/codex-sdk";
import type { Engine, EngineConfig, EngineResult } from "../engine.js";
import type { JournalSink } from "../journal.js";
import { describeRunEnding } from "./ending.js";

// CodexThreadFactory starts the Codex thread a run streams; a seam for tests.
export type CodexThreadFactory = () => Pick<Thread, "runStreamed">;

// CodexEngine drives the in-box loop with the OpenAI Codex SDK. Like the Claude
// engine it mounts agent-box as its tool surface — but Codex wires MCP servers
// and the model through its own config (~/.codex/config.toml), which the
// runtime writes before this engine runs (see writeCodexConfig in index.ts).
// Auth: the Codex SDK reads its key from the environment (CODEX_API_KEY /
// OPENAI_API_KEY), seeded via secrets.
//
// The Codex SDK has no separate system-prompt field, so the skill's persona is
// prepended to the task. The turn is streamed (thread.runStreamed) rather than
// awaited (thread.run) so each completed item is journaled as it lands
// (#2095); the result is the same thread.run() computes: the last agent
// message is the final response, turn.failed throws.
//
// Output schema (#2002): the skill's agent_card.output_schema_json is passed
// as the turn's outputSchema — the SDK's native mechanism (codex exec
