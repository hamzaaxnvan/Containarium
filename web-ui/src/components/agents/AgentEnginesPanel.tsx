'use client';

import { useState, useEffect, useCallback } from 'react';
import { RefreshCw, Loader2, CheckCircle2, AlertTriangle, HelpCircle } from 'lucide-react';
import { Server } from '@/src/types/server';
import { AgentEngine, AgentEngineReadiness, AgentEngineStatus, AgentCredentialSource, GatewayProvider } from '@/src/types/agents';
import { getClient } from '@/src/lib/api/client';

interface AgentEnginesPanelProps { server: Server; }

const ENGINE_LABELS: Record<AgentEngine, string> = {
  AGENT_ENGINE_UNSPECIFIED: 'Unspecified',
  AGENT_ENGINE_CLAUDE: 'Claude',
  AGENT_ENGINE_CODEX: 'Codex',
  AGENT_ENGINE_GEMINI: 'Gemini',
};

// Mirrors internal/gatewayprovider.Name() — same lowercase/hyphen spelling,
// so this label and the CLI's PROVIDER column never read differently for
// the same row.
const PROVIDER_LABELS: Record<GatewayProvider, string> = {
  GATEWAY_PROVIDER_UNSPECIFIED: '—',
  GATEWAY_PROVIDER_ANTHROPIC: 'anthropic',
  GATEWAY_PROVIDER_OPENAI: 'openai',
  GATEWAY_PROVIDER_GEMINI: 'gemini',
  GATEWAY_PROVIDER_GEMINI_OPENAI: 'gemini-openai',
  GATEWAY_PROVIDER_KAFEIDO: 'kafeido',
};

const SOURCE_LABELS: Record<AgentCredentialSource, string> = {
  AGENT_CREDENTIAL_SOURCE_UNSPECIFIED: '—',
  AGENT_CREDENTIAL_SOURCE_GLOBAL_KEY: 'global key',
  AGENT_CREDENTIAL_SOURCE_OWNER_KEY: 'your key',
  AGENT_CREDENTIAL_SOURCE_DIRECT_MODE: 'direct mode',
};

const GATEWAY_KEY_DOC_URL = 'https://github.com/FootprintAI/Containarium/blob/main/docs/AGENT-MODEL-GATEWAY-DESIGN.md';

function readinessBadge(readiness: AgentEngineReadiness) {
  switch (readiness) {
    case 'AGENT_ENGINE_READINESS_READY':
      return (
        <span className="inline-flex items-center gap-1 text-[var(--c-emerald)]">
          <CheckCircle2 className="h-3.5 w-3.5" /> ready
        </span>
      );
    case 'AGENT_ENGINE_READINESS_NOT_READY':
      return (
        <span className="inline-flex items-center gap-1 text-[var(--c-amber)]">
          <AlertTriangle className="h-3.5 w-3.5" /> not ready
        </span>
      );
    case 'AGENT_ENGINE_READINESS_UNKNOWN_DIRECT_MODE':
      return (
        <span className="inline-flex items-center gap-1 text-[var(--text-muted)]">
          <HelpCircle className="h-3.5 w-3.5" /> unknown (direct mode)
        </span>
      );
    default:
      return <span className="text-[var(--text-muted)]">—</span>;
  }
}

export default function AgentEnginesPanel({ server }: AgentEnginesPanelProps) {
  const [engines, setEngines] = useState<AgentEngineStatus[]>([]);
  const [keyOwner, setKeyOwner] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    try {
      const client = getClient(server);
      const resp = await client.listAgentEngines();
      setEngines(resp.engines);
      setKeyOwner(resp.keyOwner);
      setError('');
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load agent engines');
    } finally {
      setLoading(false);
    }
  }, [server]);

  useEffect(() => {
