import type { Engine } from "./engine.js";
import { stateCommand } from "./credential-state-cli.js";
export type AuthenticationSource = "oauth" | "key" | "unknown";
export class CredentialAuthenticationError extends Error {
  constructor(readonly source: AuthenticationSource = "unknown") {
    super("coding-engine authentication failed");
  }
}
export type CredentialStateCommand = (home: string, args: string[]) => unknown;
// Success updates health, never unacknowledged failure episodes. Source is
// supplied by the SDK/launch metadata, never inferred from a credential file.
export function trackClaudeCredentialState(
  engine: Engine,
