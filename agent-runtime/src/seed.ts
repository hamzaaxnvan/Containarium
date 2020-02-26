import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

// DEFAULT_SEED_DIR is where RunAgentSkill seeds the box (internal/server/
// agent_server.go buildAgentSeedScript): system_prompt.txt, token, input.json,
// agent-card.json.
export const DEFAULT_SEED_DIR = "/etc/containarium/agent";

// AgentCard mirrors the relevant fields of the seeded agent-card.json
// (grpc-gateway camelCase). Only output_schema_json is load-bearing for the
// runtime — it becomes Seed.outputSchema, which the engines enforce (#2002);
// the rest is passed through for discovery.
export interface AgentCard {
  id?: string;
  name?: string;
  capabilities?: string[];
  inputSchemaJson?: string;
  outputSchemaJson?: string;
  [k: string]: unknown;
}

// PlatformMcpConfig mirrors <seed>/platform_mcp.json — the daemon's instruction
// to mount the platform MCP beside agent-box (design decision D4). The shape is
// pinned by fixtures/platform_mcp.json, which internal/server's tests also read.
export interface PlatformMcpConfig {
  command: string;
  args: string[];
  serverUrl: string;
  // Path to the run's platform JWT; the MCP server re-reads it per request, so
  // the token value never enters a process env or a config file.
  tokenFile: string;
  // CONTAINARIUM_MCP_TOOLS allow-list, e.g. "tracker_*". Never empty (see
  // parsePlatformMcp): an empty list means "every tool" to the MCP server.
  tools: string;
