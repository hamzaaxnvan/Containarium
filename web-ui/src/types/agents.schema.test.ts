import { describe, it, expect } from 'vitest';
import { ListAgentEnginesResponseSchema } from './agents';

// #2224: this boundary is parsed, never cast — a `body as ListAgentEnginesResponse`
// would compile fine against a daemon response shaped however the server
// happens to send it; zod is what actually checks the wire contract.
describe('ListAgentEnginesResponseSchema', () => {
  it('parses a full, real-shaped response', () => {
    const body = {
      keyOwner: 'user:alice',
      engines: [
        {
          engine: 'AGENT_ENGINE_CLAUDE',
          provider: 'GATEWAY_PROVIDER_ANTHROPIC',
          readiness: 'AGENT_ENGINE_READINESS_READY',
          source: 'AGENT_CREDENTIAL_SOURCE_GLOBAL_KEY',
          isDefault: true,
          skillIds: ['hello-agent'],
        },
        {
          engine: 'AGENT_ENGINE_CODEX',
          provider: 'GATEWAY_PROVIDER_OPENAI',
          readiness: 'AGENT_ENGINE_READINESS_NOT_READY',
          reason: 'no key for provider openai; fix: containarium gateway key set user:alice --provider openai --key <value>',
        },
      ],
    };
    const parsed = ListAgentEnginesResponseSchema.parse(body);
    expect(parsed.keyOwner).toBe('user:alice');
    expect(parsed.engines).toHaveLength(2);
    expect(parsed.engines[0].isDefault).toBe(true);
    expect(parsed.engines[0].skillIds).toEqual(['hello-agent']);
    expect(parsed.engines[1].readiness).toBe('AGENT_ENGINE_READINESS_NOT_READY');
