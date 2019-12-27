import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import AgentEnginesPanel from './AgentEnginesPanel';
import { Server } from '@/src/types/server';

const listAgentEngines = vi.fn();

vi.mock('@/src/lib/api/client', () => ({
  getClient: () => ({ listAgentEngines }),
}));

const server: Server = { id: 's1', name: 'test', endpoint: 'https://example.test', token: 'tok', addedAt: Date.now() };

describe('AgentEnginesPanel', () => {
  beforeEach(() => {
    listAgentEngines.mockReset();
  });

  it('renders each engine with its readiness, source, and skills', async () => {
    listAgentEngines.mockResolvedValue({
      keyOwner: 'user:alice',
      engines: [
        {
          engine: 'AGENT_ENGINE_CLAUDE',
          provider: 'GATEWAY_PROVIDER_ANTHROPIC',
          readiness: 'AGENT_ENGINE_READINESS_READY',
          reason: '',
          source: 'AGENT_CREDENTIAL_SOURCE_GLOBAL_KEY',
          isDefault: true,
          skillIds: ['hello-agent'],
        },
        {
          engine: 'AGENT_ENGINE_CODEX',
          provider: 'GATEWAY_PROVIDER_OPENAI',
